package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/phanngoc/agent-tui/internal/agent"
)

// TestClaudeSlug pins the spelling against a folder Claude Code itself made.
func TestClaudeSlug(t *testing.T) {
	cwd := `C:\Users\phan.ngoc\AppData\Local\Temp\claude\C--Users-phan-ngoc-Documents-agent-tui\0efa1317-c904-4ba4-b304-e047fad15050\scratchpad`
	want := `C--Users-phan-ngoc-AppData-Local-Temp-claude-C--Users-phan-ngoc-Documents-agent-tui-0efa1317-c904-4ba4-b304-e047fad15050-scratchpad`
	if got := claudeSlug(cwd); got != want {
		t.Errorf("slug\n got %s\nwant %s", got, want)
	}
	if got := claudeSlug("/home/me/sbi-fpaas"); got != "-home-me-sbi-fpaas" {
		t.Errorf("posix slug = %s", got)
	}
}

// TestClaudeTasksSayWhereTheirOutputIs: a command Claude Code starts, in the
// foreground or not, comes with the places its output is being written to.
// The lines are the shape 2.1.287 sends.
func TestClaudeTasksSayWhereTheirOutputIs(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "proj")
	init, _ := json.Marshal(map[string]any{
		"type": "system", "subtype": "init", "cwd": cwd, "session_id": "f77cc28e",
	})

	d := &claudeDec{}
	var got []agent.EvTask
	emit := func(e agent.Event) {
		if tk, ok := e.(agent.EvTask); ok {
			got = append(got, tk)
		}
	}
	d.line(init, emit)
	d.line([]byte(`{"type":"system","subtype":"task_started","task_id":"bj6q99fbl","tool_use_id":"toolu_1","description":"Run foreground loop","is_backgrounded":false,"task_type":"local_bash","session_id":"f77cc28e"}`), emit)

	if len(got) != 1 {
		t.Fatalf("got %d task events, want 1", len(got))
	}
	tk := got[0]
	if tk.State != "running" || tk.Label != "Run foreground loop" {
		t.Errorf("task = %+v", tk)
	}
	want := filepath.Join(os.TempDir(), "claude", claudeSlug(cwd), "f77cc28e", "tasks", "bj6q99fbl.output")
	if len(tk.Live) == 0 || tk.Live[0] != want {
		t.Fatalf("first guess = %v, want %s", tk.Live, want)
	}
	if len(tk.Live) < 2 {
		t.Error("no fallback pattern for a folder spelled differently")
	}
}

func TestClaudeTaskFilesRefuseOddIDs(t *testing.T) {
	for _, c := range [][2]string{{"", "t1"}, {"s1", ""}, {"../x", "t1"}, {"s1", "*"}} {
		if got := claudeTaskFiles("/p", c[0], c[1]); got != nil {
			t.Errorf("session=%q task=%q gave %v; an id that is not one should give nothing", c[0], c[1], got)
		}
	}
}
