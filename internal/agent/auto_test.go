package agent

import (
	"encoding/json"
	"testing"

	"github.com/phanngoc/agent-tui/internal/session"
)

func call(t *testing.T, name string, input map[string]any) session.ToolCall {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return session.ToolCall{Name: name, Input: raw}
}

// TestAutoRunsShellCommands is the bug the user hit: in auto mode every git
// command came back waiting on an approval the session could not display.
func TestAutoRunsShellCommands(t *testing.T) {
	const root = "/home/me/project"
	for _, name := range []string{"bash", "Bash", "BASH", "shell"} {
		c := call(t, name, map[string]any{"command": "git --version"})
		if !AutoAllows(c, root) {
			t.Errorf("auto stopped to ask about %s", name)
		}
	}
	// Whatever the command is: auto's boundary is the project, and a shell
	// command names no path for it to judge.
	for _, cmd := range []string{"git pull", "git log", "npm test", "ls -la"} {
		c := call(t, "Bash", map[string]any{"command": cmd})
		if !AutoAllows(c, root) {
			t.Errorf("auto stopped to ask about %q", cmd)
		}
	}
}

func TestAutoAllowsWritesInsideTheProject(t *testing.T) {
	const root = "/home/me/project"
	c := call(t, "write_file", map[string]any{"file_path": root + "/main.go"})
	if !AutoAllows(c, root) {
		t.Error("auto asked about a write inside the project")
	}
}

// TestAutoStillGuardsTheProjectBoundary keeps the fix from turning auto into
// full: the one thing auto does enforce must survive.
func TestAutoStillGuardsTheProjectBoundary(t *testing.T) {
	const root = "/home/me/project"
	for _, p := range []string{"/etc/passwd", "/home/me/other/x.go", "/home/me/project/../x"} {
		c := call(t, "write_file", map[string]any{"file_path": p})
		if AutoAllows(c, root) {
			t.Errorf("auto allowed a write to %q, outside %q", p, root)
		}
	}
}

// TestBothEnginesAgreeOnAuto is why the rule was pulled out into one function:
// the built-in agent let a shell command run and the Claude Code broker did
// not, so the same mode meant two things depending on which engine answered.
func TestBothEnginesAgreeOnAuto(t *testing.T) {
	const root = "/home/me/project"
	e := &Executor{Root: root}

	cases := []session.ToolCall{
		call(t, "bash", map[string]any{"command": "git pull"}),
		call(t, "Bash", map[string]any{"command": "git pull"}),
		call(t, "write_file", map[string]any{"file_path": root + "/a.go"}),
		call(t, "write_file", map[string]any{"file_path": "/etc/hosts"}),
	}
	for _, c := range cases {
		ask, _ := e.ShouldAsk(c, ModeAuto, false)
		allow := AutoAllows(c, root)
		if ask == allow {
			t.Errorf("%s: built-in asks=%v while the shared rule allows=%v",
				c.Name, ask, allow)
		}
	}
}

func TestAskModeStillConfirmsEverything(t *testing.T) {
	const root = "/home/me/project"
	e := &Executor{Root: root}
	for _, name := range []string{"bash", "write_file", "edit_file"} {
		c := call(t, name, map[string]any{"command": "ls", "file_path": root + "/a"})
		if ask, _ := e.ShouldAsk(c, ModeAsk, false); !ask {
			t.Errorf("ask mode did not confirm %s", name)
		}
	}
}

func TestReadOnlyToolsAreNeverAsked(t *testing.T) {
	const root = "/home/me/project"
	e := &Executor{Root: root}
	for _, name := range []string{"read_file", "grep", "find_files"} {
		c := call(t, name, map[string]any{"path": "/etc/passwd"})
		for _, m := range []Mode{ModeAuto, ModeAsk} {
			if ask, _ := e.ShouldAsk(c, m, false); ask {
				t.Errorf("%s asked about the read-only tool %s", m, name)
			}
		}
	}
}
