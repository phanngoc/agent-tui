package engine

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/phanngoc/agent-tui/internal/agent"
)

// The allowed folders reach both CLIs as --add-dir: only those that exist,
// since both refuse one that does not, and never the project or a folder in
// it. Claude Code's --add-dir takes a list, so a flag must follow it.
func TestAllowedDirsReachTheCLIs(t *testing.T) {
	defer pinSandbox(false)()
	root := t.TempDir()
	scratch := t.TempDir()
	turn := agent.Turn{Prompt: "hi", Root: root, Mode: agent.ModeAuto,
		Dirs: []string{scratch, filepath.Join(scratch, "not-made-yet"), "/tmp-nowhere", filepath.Join(root, "sub"), root}}

	a := claudeArgv(newClaude(root), turn, nil)
	i := slices.Index(a, "--add-dir")
	if i < 0 || i+2 >= len(a) || a[i+1] != scratch || !strings.HasPrefix(a[i+2], "--") {
		t.Fatalf("claude: want --add-dir %s followed by a flag: %v", scratch, a)
	}
	if strings.Count(strings.Join(a, " "), "not-made-yet") > 0 || slices.Contains(a, "/tmp-nowhere") {
		t.Errorf("claude was handed a folder that is not there: %v", a)
	}

	c := codexArgv(newCodex(root), turn, nil)
	if j := slices.Index(c, "--add-dir"); j < 0 || c[j+1] != scratch || strings.Count(strings.Join(c, " "), "--add-dir") != 1 {
		t.Errorf("codex: want one --add-dir %s: %v", scratch, c)
	}
	turn.Mode = agent.ModePlan
	if c := codexArgv(newCodex(root), turn, nil); slices.Contains(c, "--add-dir") {
		t.Errorf("plan mode made a folder writable: %v", c)
	}
}
