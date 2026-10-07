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

// TestAutoRunsToolsThatNameNoPath: an MCP tool searching Slack came up as
// "this is outside <project>" and waited on the user, in auto, every call.
func TestAutoRunsToolsThatNameNoPath(t *testing.T) {
	const root = "/home/me/project"
	for _, c := range []session.ToolCall{
		call(t, "mcp__shizuka__message_search", map[string]any{"channel": "C0A8", "query": "*", "limit": 50}),
		call(t, "mcp__shizuka__list_projects", map[string]any{}),
		call(t, "WebFetch", map[string]any{"url": "https://example.com"}),
	} {
		if !AutoAllows(c, root) {
			t.Errorf("auto stopped to ask about %s", c.Name)
		}
	}
	// A path still counts, whoever names it.
	c := call(t, "mcp__fs__write", map[string]any{"path": "/etc/hosts"})
	if AutoAllows(c, root) {
		t.Error("auto allowed an MCP write outside the project")
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

// TestAutoReadsAnywhere: the project is the boundary for changes, not for
// looking. The case that showed it: the agent reading an image the user had
// just pasted, from the folder pasted images are kept in, outside the project.
func TestAutoReadsAnywhere(t *testing.T) {
	const root = `C:\Users\me\project`
	pasted := `C:\Users\me\AppData\Local\agent-tui\attachments\s1\paste-1.png`
	for _, c := range []session.ToolCall{
		call(t, "Read", map[string]any{"file_path": pasted}),
		call(t, "Grep", map[string]any{"pattern": "x", "path": `C:\Windows`}),
		call(t, "Glob", map[string]any{"pattern": "*.go", "path": "/etc"}),
		call(t, "read_file", map[string]any{"path": "/etc/hosts"}),
	} {
		if !AutoAllows(c, root) {
			t.Errorf("auto stopped to ask before %s looked outside the project", c.Name)
		}
	}
	// Changing something outside the project still asks.
	for _, name := range []string{"Write", "Edit", "write_file"} {
		c := call(t, name, map[string]any{"file_path": pasted})
		if AutoAllows(c, root) {
			t.Errorf("auto let %s change a file outside the project", name)
		}
	}
}

// TestAutoAllowsTheAllowedDirs: a write to a folder on the list — a clone in
// the temp folder, where the agent is making a PR to another repository — is
// not asked about; one anywhere else still is, and so is everything in ask.
func TestAutoAllowsTheAllowedDirs(t *testing.T) {
	const root = "/home/me/project"
	dirs := []string{"/tmp", "/home/me/project.worktrees"}
	for _, p := range []string{"/tmp/adm-d2/src/a.ts", "/tmp/adm-d2-pr-body.md", "/home/me/project.worktrees/fix/a.go"} {
		c := call(t, "Write", map[string]any{"file_path": p})
		if !AutoAllows(c, root, dirs...) {
			t.Errorf("auto asked about %s, in an allowed folder", p)
		}
	}
	for _, p := range []string{"/etc/hosts", "/tmpx/a", "/tmp/../etc/hosts"} {
		c := call(t, "Write", map[string]any{"file_path": p})
		if AutoAllows(c, root, dirs...) {
			t.Errorf("auto allowed %s", p)
		}
	}
	// A path relative to the project is judged against the project only.
	if c := call(t, "Write", map[string]any{"file_path": "../x"}); AutoAllows(c, root, dirs...) {
		t.Error("auto allowed a relative path out of the project")
	}

	e := &Executor{Root: root, Dirs: dirs}
	c := call(t, "write_file", map[string]any{"file_path": "/tmp/adm-d2/a.ts"})
	if ask, _ := e.ShouldAsk(c, ModeAuto, false); ask {
		t.Error("the built-in agent asked about an allowed folder in auto")
	}
	if ask, _ := e.ShouldAsk(c, ModeAsk, false); !ask {
		t.Error("ask mode stopped confirming a change in an allowed folder")
	}
	if _, err := e.resolve("/tmp/adm-d2/a.ts"); err != nil {
		t.Errorf("the built-in tools cannot reach an allowed folder: %v", err)
	}
	if _, err := e.resolve("/etc/hosts"); err == nil {
		t.Error("the built-in tools reached outside every allowed folder")
	}
}
