package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/agent"
)

// Live tests spawn the real CLIs and therefore cost real money and need real
// credentials. They are skipped unless AGENT_TUI_LIVE=1, so `go test ./...`
// stays free and offline.
func liveOrSkip(t *testing.T) {
	t.Helper()
	if os.Getenv("AGENT_TUI_LIVE") != "1" {
		t.Skip("set AGENT_TUI_LIVE=1 to run against the real CLIs")
	}
}

func liveRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "marker.go"), []byte("package marker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// runEngine drives one turn and collects everything it emitted.
func runEngine(t *testing.T, e agent.Engine, root, prompt string) []agent.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	ch := make(chan agent.Event, 256)
	go e.Run(ctx, agent.Turn{Prompt: prompt, Root: root, Mode: agent.ModeAuto}, ch)

	var events []agent.Event
	for ev := range ch {
		events = append(events, ev)
	}
	return events
}

func checkTurn(t *testing.T, events []agent.Event, wantText string) {
	t.Helper()
	orderOK(t, events)

	var done *agent.EvDone
	for i := range events {
		if d, ok := events[i].(agent.EvDone); ok {
			done = &d
		}
	}
	if done == nil {
		t.Fatal("the turn never reported EvDone")
	}
	if done.Err != nil {
		t.Fatalf("turn failed: %v", done.Err)
	}
	if ids := sessionIDs(events); len(ids) == 0 || ids[0] == "" {
		t.Error("no external session id was reported, so the turn cannot be resumed")
	}

	var all strings.Builder
	for _, m := range assistants(events) {
		all.WriteString(m.Text)
	}
	if !strings.Contains(strings.ToUpper(all.String()), wantText) {
		t.Errorf("assistant text %q does not contain %q", all.String(), wantText)
	}
	if in, out, _ := usage(events); in == 0 && out == 0 {
		t.Error("no token usage was reported")
	}
}

func TestLiveClaude(t *testing.T) {
	liveOrSkip(t)
	e := newClaude(liveRoot(t)) // auto mode: no broker, this test has no UI to ask
	if !e.Available() {
		t.Skip(e.Detail())
	}
	checkTurn(t, runEngine(t, e, liveRoot(t), "Reply with exactly: PONG"), "PONG")
}

func TestLiveCodex(t *testing.T) {
	liveOrSkip(t)
	root := liveRoot(t)
	e := newCodex(root)
	if !e.Available() {
		t.Skip(e.Detail())
	}
	checkTurn(t, runEngine(t, e, root, "Reply with exactly: PONG"), "PONG")
}

func TestLiveOpenCode(t *testing.T) {
	liveOrSkip(t)
	root := liveRoot(t)
	e := newOpenCode(root)
	if !e.Available() {
		t.Skip(e.Detail())
	}
	checkTurn(t, runEngine(t, e, root, "Reply with exactly: PONG"), "PONG")
}

// buildSelf compiles the real binary so the approval broker has something with
// a --permission-broker flag to re-execute; the test binary has no such flag.
func buildSelf(t *testing.T) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "agent-tui")
	out, err := exec.Command("go", "build", "-o", bin, "github.com/phanngoc/agent-tui/cmd/agent-tui").CombinedOutput()
	if err != nil {
		t.Fatalf("building the broker binary failed: %v\n%s", err, out)
	}
	prev := executable
	executable = func() (string, error) { return bin, nil }
	t.Cleanup(func() { executable = prev })
}

// TestLiveClaudeAsksBeforeActing exercises the whole approval path: the CLI
// asks an MCP tool, that tool is this binary in broker mode, and the broker
// forwards the question over a unix socket to the code below.
func TestLiveClaudeAsksBeforeActing(t *testing.T) {
	liveOrSkip(t)
	root := liveRoot(t)
	e := newClaude(root)
	if !e.Available() {
		t.Skip(e.Detail())
	}
	buildSelf(t)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	ch := make(chan agent.Event, 256)
	go e.Run(ctx, agent.Turn{Prompt: "Create a file named approved.txt containing HELLO, then reply DONE.", Root: root, Mode: agent.ModeAsk}, ch)

	asked := 0
	for ev := range ch {
		if ap, ok := ev.(agent.EvApproval); ok {
			asked++
			ap.Reply <- agent.Allow
		}
	}
	if asked == 0 {
		t.Fatal("the CLI wrote a file without asking; the broker is not wired up")
	}
	if _, err := os.Stat(filepath.Join(root, "approved.txt")); err != nil {
		t.Errorf("approved write did not happen: %v", err)
	}
}

// TestLiveClaudeHonoursDenial is the other half: saying no must stop the write.
func TestLiveClaudeHonoursDenial(t *testing.T) {
	liveOrSkip(t)
	root := liveRoot(t)
	e := newClaude(root)
	if !e.Available() {
		t.Skip(e.Detail())
	}
	buildSelf(t)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	ch := make(chan agent.Event, 256)
	go e.Run(ctx, agent.Turn{Prompt: "Create a file named denied.txt containing HELLO.", Root: root, Mode: agent.ModeAsk}, ch)

	asked := 0
	for ev := range ch {
		if ap, ok := ev.(agent.EvApproval); ok {
			asked++
			ap.Reply <- agent.Deny
		}
	}
	if asked == 0 {
		t.Fatal("no approval was requested")
	}
	if _, err := os.Stat(filepath.Join(root, "denied.txt")); err == nil {
		t.Error("the file was written even though the call was denied")
	}
}

// TestLiveCodexResume reproduces the reported failure: the second turn of a
// codex session passed -C and --sandbox to `codex exec resume`, which rejects
// both, so every session died with "unexpected argument '-C' found".
func TestLiveCodexResume(t *testing.T) {
	liveOrSkip(t)
	root := liveRoot(t)
	e := newCodex(root)
	if !e.Available() {
		t.Skip(e.Detail())
	}

	first := runEngine(t, e, root, "Remember the word BANANA. Reply with just: OK")
	checkTurn(t, first, "OK")

	id := sessionIDs(first)[0]
	if id == "" {
		t.Fatal("no thread id to resume")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	ch := make(chan agent.Event, 256)
	go e.Run(ctx, agent.Turn{
		Prompt:     "What word did I ask you to remember? Reply with just that word.",
		Root:       root,
		ExternalID: id,
		Mode:       agent.ModeAuto,
	}, ch)

	var second []agent.Event
	for ev := range ch {
		second = append(second, ev)
	}
	checkTurn(t, second, "BANANA")
}

// TestLiveAutoAsksOutsideTheProject is the reported problem: in auto mode a
// write outside the project came back denied with nobody asked, because
// Claude Code's acceptEdits only settles work inside the workspace and there
// was no prompt handler for the rest.
func TestLiveAutoAsksOutsideTheProject(t *testing.T) {
	liveOrSkip(t)
	root := liveRoot(t)
	outside := filepath.Join(t.TempDir(), "outside.txt")

	e := newClaude(root)
	if !e.Available() {
		t.Skip(e.Detail())
	}
	buildSelf(t)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	ch := make(chan agent.Event, 256)
	go e.Run(ctx, agent.Turn{
		Prompt: "Write the word HELLO to ./inside.txt and also to " + outside + ". Then reply DONE.",
		Root:   root,
		Mode:   agent.ModeAuto,
	}, ch)

	var asked []string
	for ev := range ch {
		if ap, ok := ev.(agent.EvApproval); ok {
			asked = append(asked, ap.Reason)
			ap.Reply <- agent.Allow
		}
	}

	// Inside the project must not have been asked about; outside must have.
	if len(asked) == 0 {
		t.Fatal("auto mode never asked about the write outside the project")
	}
	for _, reason := range asked {
		if !strings.Contains(reason, "outside") {
			t.Errorf("asked for the wrong reason: %q", reason)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "inside.txt")); err != nil {
		t.Errorf("the write inside the project did not happen: %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("the approved write outside the project did not happen: %v", err)
	}
}
