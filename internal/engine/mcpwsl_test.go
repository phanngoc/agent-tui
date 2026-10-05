package engine

import (
	"strings"
	"testing"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// A CLI in a distribution keeps the remote servers and agent-tui's own, run
// by its /mnt path; a Windows command-line server is left out. On this
// machine nothing changes.
func TestServersForWSL(t *testing.T) {
	in := map[string]any{
		"agent-tui": map[string]any{"type": "stdio", "command": `C:\Users\me\go\bin\agent-tui.exe`, "args": []string{"kit-mcp", "-root", `\wsl.localhost\Ubuntu\home\me\app`}},
		"datadog":   map[string]any{"type": "http", "url": "https://mcp.datadoghq.com/v1/mcp", "headers": map[string]string{"Authorization": "Bearer t"}},
		"backlog":   map[string]any{"type": "stdio", "command": "npx", "args": []string{"-y", "backlog-mcp-server"}},
	}
	if got := serversFor(vfs.NewLocal(""), in); len(got) != 3 {
		t.Fatalf("local: %d servers; want all 3", len(got))
	}
	got := serversFor(vfs.NewWSL("Ubuntu"), in)
	if _, ok := got["backlog"]; ok {
		t.Error("a Windows command-line server was handed to the distribution")
	}
	if got["datadog"] == nil {
		t.Error("the remote server was dropped")
	}
	kit, _ := got["agent-tui"].(map[string]any)
	if kit == nil || kit["command"] != "/mnt/c/Users/me/go/bin/agent-tui.exe" {
		t.Fatalf("agent-tui's server = %v; want it run by its /mnt path", got["agent-tui"])
	}
	if in["agent-tui"].(map[string]any)["command"] == kit["command"] {
		t.Error("the input was changed in place")
	}
}

// Claude Code's own schedulers are withheld, and the list flag never ends
// the command line, where it would take the prompt for one of its items.
func TestClaudeArgvWithholdsSchedulersAndEndsWithThePrompt(t *testing.T) {
	c := &CLI{id: IDClaude}
	turn := agent.Turn{Prompt: "check the logs every hour"}
	for _, servers := range []map[string]any{nil, {"x": map[string]any{"type": "http", "url": "https://x"}}} {
		turn.MCPServers = servers
		a := claudeArgv(c, turn, nil)
		if a[len(a)-1] != turn.PromptText() {
			t.Fatalf("the last argument is %q, not the prompt", a[len(a)-1])
		}
		i := indexOf(a, "--disallowedTools")
		if i < 0 || !strings.Contains(a[i+1], "Skill(schedule)") || !strings.Contains(a[i+1], "RemoteTrigger") {
			t.Fatalf("schedulers not withheld: %q", a)
		}
		if i+2 >= len(a)-1 || !strings.HasPrefix(a[i+2], "--") {
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
