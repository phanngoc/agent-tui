package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/task"
)

// TestCtrlKOpensTheOneRunningCommand: with a single command running, the
// question is what it is doing, so ctrl+k answers it without a list between.
func TestCtrlKOpensTheOneRunningCommand(t *testing.T) {
	m := newTestModel(t)
	m.tasks.Adopt("old", "finished earlier", m.mgr.Active().ID)
	m.tasks.Update("old", task.Done, "")
	m.tasks.Adopt("b1", "Run the e2e batch", m.mgr.Active().ID)

	press(t, m, "ctrl+k")
	if m.overlay != overlayTasks || m.taskOpen != "b1" {
		t.Fatalf("overlay=%v open=%q, want the running command opened", m.overlay, m.taskOpen)
	}
	press(t, m, "esc")
	if m.overlay != overlayTasks || m.taskOpen != "" {
		t.Error("esc should step back to the list")
	}
}

func TestStatusBarSaysHowToWatch(t *testing.T) {
	m := newTestModel(t)
	m.tasks.Adopt("b1", "build", m.mgr.Active().ID)
	if got := stripANSI(m.View().Content); !strings.Contains(got, "1 running ctrl+k") {
		t.Errorf("the status bar does not say how to look:\n%s", got)
	}
}

// TestRunningCommandOutputIsWatchable is the bug: a command Claude Code was
// running showed "●1 running" for ten minutes and nothing else.
func TestRunningCommandOutputIsWatchable(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	path := filepath.Join(t.TempDir(), "bj6q99fbl.output")
	if err := os.WriteFile(path, []byte("\x1b[32mPASS\x1b[0m case 1\nrunning case 2\r 40%\r 80%\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m.Update(agentMsg{sess: s, ch: make(chan agent.Event, 1), ev: agent.EvTask{
		ID: "bj6q99fbl", Label: "Run foreground loop", State: "running", Live: []string{path},
	}})
	press(t, m, "ctrl+k")

	deadline := time.Now().Add(3 * time.Second)
	var out string
	for time.Now().Before(deadline) {
		out = stripANSI(m.View().Content)
		if strings.Contains(out, "PASS case 1") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, want := range []string{"Run foreground loop", "live", "PASS case 1", " 80%"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q from the output view:\n%s", want, out)
		}
	}
	if strings.Contains(out, "40%") {
		t.Error("an overwritten progress frame is still shown")
	}
	m.tasks.Update("bj6q99fbl", task.Done, "")
}

// TestTheListShowsWhatIsRunning: a long turn runs dozens of commands, and the
// few still going were lost at the bottom of a column of ticks. The list is
// what is running; the rest are one key away.
func TestTheListShowsWhatIsRunning(t *testing.T) {
	m := newTestModel(t)
	owner := m.mgr.Active().ID
	for _, id := range []string{"d1", "d2", "d3"} {
		m.tasks.Adopt(id, "finished "+id, owner)
		m.tasks.Update(id, task.Done, "")
	}
	m.tasks.Adopt("r1", "Lint the touched batch files", owner)
	m.tasks.Adopt("r2", "batch full suite", owner)

	press(t, m, "ctrl+k")
	out := stripANSI(m.View().Content)
	if strings.Contains(out, "finished d1") {
		t.Errorf("a finished command is in the list:\n%s", out)
	}
	for _, want := range []string{"Lint the touched batch files", "batch full suite", "3 finished commands · a shows them"} {
		if !strings.Contains(out, want) {
			t.Errorf("the list is missing %q:\n%s", want, out)
		}
	}

	press(t, m, "a")
	if out := stripANSI(m.View().Content); !strings.Contains(out, "finished d1") {
		t.Errorf("a did not show the finished commands:\n%s", out)
	}
	press(t, m, "a")
	if out := stripANSI(m.View().Content); strings.Contains(out, "finished d1") {
		t.Errorf("a second a did not hide them again:\n%s", out)
	}

	// One finishing while the list is open leaves it, and the selection
	// stays on a row that exists.
	press(t, m, "down")
	m.tasks.Update("r2", task.Done, "")
	m.View()
	press(t, m, "enter")
	if m.taskOpen != "r1" {
		t.Errorf("enter opened %q, want the one still running", m.taskOpen)
	}
}
