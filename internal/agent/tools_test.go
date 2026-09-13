package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/fsx"
	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/task"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

func newExec(t *testing.T, files map[string]string) *Executor {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fsys := vfs.NewLocal(root)
	ix := fsx.NewIndex(fsys, root, 0)
	ix.Build()
	return &Executor{FS: fsys, Root: root, Index: ix, MaxBytes: 1 << 20, Workers: 2}
}

func run(t *testing.T, e *Executor, name string, in any) (string, bool) {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	return e.Run(context.Background(), name, raw)
}

func TestPathEscapeRefused(t *testing.T) {
	e := newExec(t, map[string]string{"a.go": "package a"})
	for _, p := range []string{"../outside.txt", "../../etc/passwd", "a/../../x"} {
		out, isErr := run(t, e, "read_file", map[string]any{"path": p})
		if !isErr || !strings.Contains(out, "outside the project root") {
			t.Errorf("read_file(%q) = %q, isErr=%v; want a refusal", p, out, isErr)
		}
	}
}

func TestReadFileLineRange(t *testing.T) {
	e := newExec(t, map[string]string{"a.go": "one\ntwo\nthree\nfour\n"})
	out, isErr := run(t, e, "read_file", map[string]any{"path": "a.go", "start_line": 2, "end_line": 3})
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if !strings.Contains(out, "two") || !strings.Contains(out, "three") {
		t.Errorf("missing requested lines: %q", out)
	}
	if strings.Contains(out, "four") {
		t.Errorf("returned lines outside the range: %q", out)
	}
	if !strings.Contains(out, "lines 2-3 of 4") {
		t.Errorf("missing the range header: %q", out)
	}
}

func TestEditFileRequiresUniqueMatch(t *testing.T) {
	e := newExec(t, map[string]string{"a.go": "x := 1\nx := 1\n"})

	out, isErr := run(t, e, "edit_file", map[string]any{
		"path": "a.go", "old_string": "x := 1", "new_string": "x := 2",
	})
	if !isErr || !strings.Contains(out, "appears 2 times") {
		t.Fatalf("ambiguous edit should be refused, got %q (isErr=%v)", out, isErr)
	}
	// The file must be untouched after a refusal.
	b, _ := os.ReadFile(filepath.Join(e.Root, "a.go"))
	if string(b) != "x := 1\nx := 1\n" {
		t.Errorf("file changed despite the refusal: %q", b)
	}

	out, isErr = run(t, e, "edit_file", map[string]any{
		"path": "a.go", "old_string": "x := 1", "new_string": "x := 2", "replace_all": true,
	})
	if isErr {
		t.Fatalf("replace_all should succeed: %s", out)
	}
	b, _ = os.ReadFile(filepath.Join(e.Root, "a.go"))
	if string(b) != "x := 2\nx := 2\n" {
		t.Errorf("content = %q", b)
	}
}

func TestEditFileMissingOldString(t *testing.T) {
	e := newExec(t, map[string]string{"a.go": "hello"})
	out, isErr := run(t, e, "edit_file", map[string]any{
		"path": "a.go", "old_string": "nope", "new_string": "x",
	})
	if !isErr || !strings.Contains(out, "not found") {
		t.Errorf("got %q (isErr=%v)", out, isErr)
	}
}

func TestGrepToolFindsMatches(t *testing.T) {
	e := newExec(t, map[string]string{
		"a.go":     "package a\nconst Token = 1\n",
		"sub/b.go": "// Token here\n",
	})
	out, isErr := run(t, e, "grep", map[string]any{"query": "Token"})
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if !strings.Contains(out, "a.go:2") || !strings.Contains(out, "sub/b.go:1") {
		t.Errorf("missing expected hits: %q", out)
	}

	scoped, _ := run(t, e, "grep", map[string]any{"query": "Token", "path": "sub"})
	if strings.Contains(scoped, "a.go:2") {
		t.Errorf("path scoping leaked results from outside sub/: %q", scoped)
	}
}

func TestFindFilesTool(t *testing.T) {
	e := newExec(t, map[string]string{"internal/search/search.go": "x"})
	out, isErr := run(t, e, "find_files", map[string]any{"query": "intsearch"})
	if isErr || !strings.Contains(out, "internal/search/search.go") {
		t.Errorf("got %q (isErr=%v)", out, isErr)
	}
}

func TestApprovalGating(t *testing.T) {
	e := newExec(t, nil)

	inProject := json.RawMessage(`{"path":"a.go"}`)
	outside := json.RawMessage(`{"path":"/etc/hosts"}`)

	// Ask mode confirms every change.
	for _, name := range []string{"write_file", "edit_file", "bash"} {
		call := session.ToolCall{Name: name, Input: inProject}
		if ask, _ := e.ShouldAsk(call, ModeAsk, false); !ask {
			t.Errorf("%s should be confirmed in ask mode", name)
		}
		if ask, _ := e.ShouldAsk(call, ModeFull, false); ask {
			t.Errorf("%s should not be confirmed in full mode", name)
		}
	}

	// Auto acts inside the project and asks about anything outside it, which is
	// the boundary the mode promises.
	inside := session.ToolCall{Name: "write_file", Input: inProject}
	if ask, _ := e.ShouldAsk(inside, ModeAuto, false); ask {
		t.Error("auto mode should write inside the project without asking")
	}
	out := session.ToolCall{Name: "write_file", Input: outside}
	ask, reason := e.ShouldAsk(out, ModeAuto, false)
	if !ask {
		t.Error("auto mode should ask before writing outside the project")
	}
	if !strings.Contains(reason, "outside") {
		t.Errorf("the reason should say why: %q", reason)
	}

	// Reading is never confirmed.
	for _, name := range []string{"read_file", "grep", "list_dir", "find_files"} {
		call := session.ToolCall{Name: name, Input: outside}
		if ask, _ := e.ShouldAsk(call, ModeAsk, false); ask {
			t.Errorf("%s should not require approval", name)
		}
	}

	// Trust granted earlier in the run skips the question.
	if ask, _ := e.ShouldAsk(out, ModeAsk, true); ask {
		t.Error("a trusted run should not keep asking")
	}

	// Trusting the rest of a run is the agent's business, not the executor's:
	// the executor is shared between concurrent sessions, so a flag on it would
	// leak one conversation's decision into another.
}

// TestPlanModeOffersNoWritingTools is the guarantee behind plan mode: a tool
// that is never offered cannot be called, which is stronger than asking the
// model to hold back.
func TestPlanModeOffersNoWritingTools(t *testing.T) {
	e := newExec(t, nil)

	names := func(mode Mode) map[string]bool {
		out := map[string]bool{}
		for _, d := range e.Defs(mode) {
			out[d.OfTool.Name] = true
		}
		return out
	}

	plan := names(ModePlan)
	for _, forbidden := range []string{"write_file", "edit_file", "bash", "task_stop"} {
		if plan[forbidden] {
			t.Errorf("plan mode offers %s", forbidden)
		}
	}
	for _, allowed := range []string{"read_file", "list_dir", "find_files", "grep", "ask_user"} {
		if !plan[allowed] {
			t.Errorf("plan mode is missing %s", allowed)
		}
	}

	for _, mode := range []Mode{ModeAsk, ModeAuto, ModeFull} {
		if !names(mode)["write_file"] {
			t.Errorf("%v mode should be able to write", mode)
		}
	}
}

func TestBashRunsInRoot(t *testing.T) {
	e := newExec(t, map[string]string{"marker.txt": "hi"})
	out, isErr := run(t, e, "bash", map[string]any{"command": "ls"})
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if !strings.Contains(out, "marker.txt") {
		t.Errorf("bash did not run in the project root: %q", out)
	}
}

func TestUnknownToolIsAnError(t *testing.T) {
	e := newExec(t, nil)
	if out, isErr := run(t, e, "launch_missiles", map[string]any{}); !isErr {
		t.Errorf("unknown tool should error, got %q", out)
	}
}

func TestToolDefsAreWellFormed(t *testing.T) {
	e := newExec(t, nil)
	defs := e.Defs(ModeAuto)
	seen := map[string]bool{}
	for _, d := range defs {
		if d.OfTool == nil || d.OfTool.Name == "" {
			t.Fatalf("malformed tool definition: %+v", d)
		}
		if seen[d.OfTool.Name] {
			t.Errorf("tool %s is declared twice", d.OfTool.Name)
		}
		seen[d.OfTool.Name] = true
		if len(d.OfTool.InputSchema.Required) == 0 && d.OfTool.Name != "list_dir" {
			t.Errorf("tool %s declares no required fields", d.OfTool.Name)
		}
	}
	for _, want := range []string{
		"read_file", "list_dir", "find_files", "grep",
		"write_file", "edit_file", "bash",
		"ask_user", "task_output", "task_stop",
	} {
		if !seen[want] {
			t.Errorf("tool %s is missing", want)
		}
	}
}

func TestBackgroundBashReturnsImmediately(t *testing.T) {
	e := newExec(t, nil)
	e.Tasks = task.NewRegistry()
	e.Session = "sess-1"

	start := time.Now()
	out, isErr := run(t, e, "bash", map[string]any{
		"command": "sleep 5; echo slow", "run_in_background": true,
	})
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	// The point of backgrounding is not waiting for it.
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("backgrounding blocked for %v", elapsed)
	}

	id := taskIDFrom(out)
	if id == "" {
		t.Fatalf("no task id in %q", out)
	}
	tk := e.Tasks.Get(id)
	if tk == nil || !tk.Live() {
		t.Fatalf("task %q is not running: %+v", id, tk)
	}
	tk.Stop()
}

func TestTaskOutputAndStop(t *testing.T) {
	e := newExec(t, nil)
	e.Tasks = task.NewRegistry()

	out, _ := run(t, e, "bash", map[string]any{
		"command": "echo hello-from-background", "run_in_background": true,
	})
	id := taskIDFrom(out)

	deadline := time.After(10 * time.Second)
	for e.Tasks.Get(id).Live() {
		select {
		case <-e.Tasks.Changed():
		case <-time.After(30 * time.Millisecond):
		case <-deadline:
			t.Fatal("the background command never finished")
		}
	}

	got, isErr := run(t, e, "task_output", map[string]any{"task_id": id})
	if isErr || !strings.Contains(got, "hello-from-background") {
		t.Errorf("task_output = %q (isErr=%v)", got, isErr)
	}
	if !strings.Contains(got, "done") {
		t.Errorf("task_output should report the state: %q", got)
	}

	if bad, isErr := run(t, e, "task_output", map[string]any{"task_id": "nope"}); !isErr {
		t.Errorf("an unknown id should be an error, got %q", bad)
	}
	if _, isErr := run(t, e, "task_stop", map[string]any{"task_id": id}); isErr {
		t.Error("stopping a finished task should not be an error")
	}
}

func TestBackgroundNeedsARegistry(t *testing.T) {
	e := newExec(t, nil) // no Tasks
	out, isErr := run(t, e, "bash", map[string]any{"command": "true", "run_in_background": true})
	if !isErr {
		t.Errorf("without a registry this must fail loudly, got %q", out)
	}
}

// taskIDFrom pulls the id out of "started in the background as <id>; …".
func taskIDFrom(s string) string {
	fields := strings.Fields(s)
	for i, f := range fields {
		if f == "as" && i+1 < len(fields) {
			return strings.Trim(fields[i+1], ";,.")
		}
	}
	return ""
}
