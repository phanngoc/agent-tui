package ui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/session"
)

// A turn that has been thinking for seven minutes and one that has been
// thinking for seven seconds read the same without a clock, and only one of
// them is worth interrupting.
func TestTheStatusBarClocksTheTurn(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	s.Busy, s.Status = true, "thinking"
	s.Started = time.Now().Add(-7*time.Minute - 52*time.Second)

	out := stripANSI(m.statusBar())
	if !strings.Contains(out, "7m 52s") {
		t.Errorf("the status bar does not say how long it has been going:\n%s", out)
	}

	// An idle session has nothing to count.
	s.Busy, s.Started = false, time.Time{}
	if out := stripANSI(m.statusBar()); strings.Contains(out, "s ") && strings.Contains(out, "thinking") {
		t.Errorf("an idle session is still counting:\n%s", out)
	}
}

// The finished calls say what they took. The one being waited on is the one
// worth asking, and it said nothing until now.
func TestARunningCallIsClocked(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	in, _ := json.Marshal(map[string]any{"command": "go test ./..."})
	call := session.ToolCall{ID: "t1", Name: "Bash", Input: in}
	s.Messages = append(s.Messages, session.Message{
		Role: session.RoleAssistant, Tools: []session.ToolCall{call},
	})
	m.Update(agentMsg{sess: s, ev: agent.EvToolStart{Call: call}})
	s.RunAt = time.Now().Add(-95 * time.Second)

	out := stripANSI(m.transcript(80))
	if !strings.Contains(out, "1m 35s") {
		t.Errorf("the running call has no clock:\n%s", out)
	}

	// Once it is done the clock is replaced by what it took, not added to.
	call.Done, call.Elapsed, call.Result = true, 3*time.Second, "ok\n"
	m.Update(agentMsg{sess: s, ev: agent.EvToolDone{Call: call}})
	out = stripANSI(m.transcript(80))
	if strings.Contains(out, "1m 35s") {
		t.Errorf("a finished call is still counting:\n%s", out)
	}
	if !strings.Contains(out, "3.0s") {
		t.Errorf("the finished call does not say what it took:\n%s", out)
	}
}

// A `!` command gets one too. It is the case where the wait is longest and the
// session is not even marked busy.
func TestARunningShellIsClocked(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	s.Messages = append(s.Messages, session.Message{
		Role: session.RoleUser,
		Shell: &session.ShellRun{
			Command: "go test ./...", Where: "host",
			Started: time.Now().Add(-42 * time.Second),
		},
	})
	s.Running = 1
	m.invalidateChat()

	out := stripANSI(m.transcript(80))
	if !strings.Contains(out, "running…") || !strings.Contains(out, "42s") {
		t.Errorf("the running command has no clock:\n%s", out)
	}
}

// The transcript's committed half is cached, and a running call's line is in
// it — so a clock in there needs the cache to let go once a second, and only
// while there is a clock.
func TestTheCacheLetsGoOnlyWhileAClockRuns(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	s.Messages = append(s.Messages, session.Message{Role: session.RoleUser, Text: "hỏi"})
	m.invalidateChat()

	if clockKey(s) != "" {
		t.Error("an idle session is redrawing for a clock it does not have")
	}
	first := m.transcript(80)
	if m.transcript(80) != first {
		t.Error("an idle transcript was rebuilt")
	}

	s.RunAt = time.Now()
	if clockKey(s) == "" {
		t.Error("a session with a running call is not redrawing")
	}
}

// The redraw keeps itself alive while anything is running, which is not the
// same as while the agent is working: a `!` command leaves the session idle.
func TestTheTickFollowsWhatIsRunning(t *testing.T) {
	m := newTestModel(t)
	if m.ticking() {
		t.Error("an idle app is still redrawing")
	}

	s := m.mgr.Active()
	s.Running = 1
	if !m.ticking() {
		t.Error("a running ! command does not keep the redraw alive")
	}
	s.Running = 0

	s.Busy = true
	if !m.ticking() {
		t.Error("a busy session does not keep the redraw alive")
	}
}

func TestRunningReadsAsAClock(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		ago  time.Duration
		want string
	}{
		{0, "0s"},
		{12 * time.Second, "12s"},
		{7*time.Minute + 52*time.Second, "7m 52s"},
		{2*time.Hour + 5*time.Minute, "2h 5m"},
	} {
		if got := running(now.Add(-tc.ago)); got != tc.want {
			t.Errorf("after %s the clock reads %q, want %q", tc.ago, got, tc.want)
		}
	}
	if got := running(time.Time{}); got != "" {
		t.Errorf("something that never started reads %q", got)
	}
}
