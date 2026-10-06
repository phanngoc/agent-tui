package engine

import (
	"strings"
	"testing"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// A CLI in a distribution is handed every server as agent-tui.exe by its
// /mnt path: its own tools as they are, the others through mcp-proxy, which
// connects from Windows with the sign-ins kept there, so no token goes to the
// distribution. A container keeps only the remote servers. On this machine
// nothing changes.
func TestServersForWSL(t *testing.T) {
	root := `\\wsl.localhost\Ubuntu\home\me\app`
	in := map[string]any{
		"agent-tui": map[string]any{"type": "stdio", "command": `C:\Users\me\go\bin\agent-tui.exe`, "args": []string{"kit-mcp", "-root", root}},
		"datadog":   map[string]any{"type": "http", "url": "https://mcp.datadoghq.com/v1/mcp", "headers": map[string]string{"Authorization": "Bearer t"}},
		"backlog":   map[string]any{"type": "stdio", "command": "npx", "args": []string{"-y", "backlog-mcp-server"}},
	}
	if got := serversFor(vfs.NewLocal(""), in, ""); len(got) != 3 {
		t.Fatalf("local: %d servers; want all 3", len(got))
	}
	box := serversFor(vfs.NewDocker("c", "", "/app"), in, "/app")
	if len(box) != 1 || box["datadog"] == nil {
		t.Errorf("container: %v; want only the remote server", box)
	}
	got := serversFor(vfs.NewWSL("Ubuntu"), in, "/home/me/app")
	const exe = "/mnt/c/Users/me/go/bin/agent-tui.exe"
	for _, name := range []string{"datadog", "backlog"} {
		def, _ := got[name].(map[string]any)
		args, _ := def["args"].([]string)
		if def == nil || def["command"] != exe || strings.Join(args, " ") != "mcp-proxy -root "+root+" -name "+name {
			t.Errorf("%s = %v; want agent-tui.exe mcp-proxy for it", name, got[name])
		}
		if _, leaked := def["headers"]; leaked {
			t.Errorf("%s: the token went to the distribution", name)
		}
	}
	kit, _ := got["agent-tui"].(map[string]any)
	if kit == nil || kit["command"] != exe {
		t.Fatalf("agent-tui's server = %v; want it run by its /mnt path", got["agent-tui"])
	}
	if in["agent-tui"].(map[string]any)["command"] == kit["command"] {
		t.Error("the input was changed in place")
	}
}

// Claude Code's own schedulers are withheld, the list flag never ends the
// command line, and the prompt is not on it at all: it goes in on stdin.
func TestClaudeArgvWithholdsSchedulers(t *testing.T) {
	c := &CLI{id: IDClaude}
	turn := agent.Turn{Prompt: "check the logs every hour"}
	for _, servers := range []map[string]any{nil, {"x": map[string]any{"type": "http", "url": "https://x"}}} {
		turn.MCPServers = servers
		a := claudeArgv(c, turn, nil)
		if indexOf(a, turn.PromptText()) >= 0 {
			t.Fatalf("the prompt is on the command line: %q", a)
		}
		i := indexOf(a, "--disallowedTools")
		if i < 0 || !strings.Contains(a[i+1], "Skill(schedule)") || !strings.Contains(a[i+1], "RemoteTrigger") {
			t.Fatalf("schedulers not withheld: %q", a)
		}
		if i+2 >= len(a) || !strings.HasPrefix(a[i+2], "--") {
			t.Fatalf("the list flag is not followed by another flag: %q", a)
		}
	}
}

func indexOf(a []string, s string) int {
	for i, x := range a {
		if x == s {
			return i
		}
	}
	return -1
}
