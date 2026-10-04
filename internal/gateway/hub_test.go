package gateway

import (
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/session"
)

type local struct{ got []Command }

func (l *local) Handle(c Command) error { l.got = append(l.got, c); return nil }

func TestRouteGoesToTheHolderElseLocal(t *testing.T) {
	h := NewHub()
	l := &local{}
	h.Local = l
	p := h.Join("tui", "/repo", 1)
	h.Hold(p.ID, []string{"s1"})
	cmds := h.Attach(p)

	if owner, err := h.Route(Command{Type: CmdPrompt, Session: "s1", Text: "hi"}); err != nil || owner != p.ID {
		t.Fatalf("owner %s %v", owner, err)
	}
	select {
	case c := <-cmds:
		if c.Text != "hi" {
			t.Fatal(c)
		}
	default:
		t.Fatal("the holder got nothing")
	}
	if owner, _ := h.Route(Command{Type: CmdPrompt, Session: "s2"}); owner != "gateway" || len(l.got) != 1 {
		t.Fatalf("local: %s %v", owner, l.got)
	}
}

func TestLiveFollowsATurnAndReplays(t *testing.T) {
	h := NewHub()
	pub := func(typ string, data any) { h.Publish(New(typ, "s", data)) }
	pub(EvTurnStarted, TurnData{Prompt: "go", Engine: "api"})
	pub(EvTextDelta, TextData{"Hel"})
	pub(EvTextDelta, TextData{"lo"})
	call := session.ToolCall{ID: "c1", Name: "bash"}
	pub(EvToolStart, ToolData{call})
	pub(EvApprovalRequest, ApprovalData{ID: "a1", Call: call})

	l := h.LiveAll()["s"]
	if !l.Busy || l.Partial != "Hello" || len(l.Running) != 1 || len(l.Approvals) != 1 || l.Prompt != "go" {
		t.Fatalf("live: %+v", l)
	}
	pub(EvApprovalDone, ResolvedData{ID: "a1"})
	pub(EvTurnDone, TurnData{})
	if l := h.LiveAll()["s"]; l.Busy || len(l.Approvals) != 0 {
		t.Fatalf("after: %+v", l)
	}

	replay, ch, cancel := h.Subscribe(3)
	defer cancel()
	if len(replay) != 4 || replay[0].Seq != 4 {
		t.Fatalf("replay %d from %d", len(replay), replay[0].Seq)
	}
	h.Publish(New(EvStatus, "s", TextData{"x"}))
	select {
	case e := <-ch:
		if e.Seq != 8 {
			t.Fatal(e.Seq)
		}
	case <-time.After(time.Second):
		t.Fatal("no live event")
	}
}

func TestFromAgentLeavesQuestionsToTheCaller(t *testing.T) {
	if FromAgent("s", agent.EvApproval{}) != nil {
		t.Fatal("an approval was translated without an id")
	}
	if e := FromAgent("s", agent.EvTextDelta{Text: "x"}); e == nil || e.Type != EvTextDelta {
		t.Fatal(e)
	}
	if VerdictOf(VerdictName(agent.AllowAll)) != agent.AllowAll || VerdictOf("junk") != agent.Deny {
		t.Fatal("verdicts do not round-trip")
	}
}

func TestFinishedGatewayTurnsAreOfferedToTerminalsInTheProject(t *testing.T) {
	h := NewHub()
	here := h.Join("tui", `C:\code\x`, 1)
	elsewhere := h.Join("tui", `C:\code\y`, 2)
	a, b := h.Attach(here), h.Attach(elsewhere)
	pub := func(busy bool, origin string) {
		e := New(EvSessionUpdated, "s9", Summary{ID: "s9", Root: "c:/code/x/", Busy: busy})
		e.Origin = origin
		h.Publish(e)
	}
	pub(true, "gateway")  // mid-turn: not yet
	pub(false, here.ID)   // a terminal's own update: never
	pub(false, "gateway") // finished: offered
	select {
	case c := <-a:
		if c.Type != CmdOpen || c.Session != "s9" {
			t.Fatal(c)
		}
	default:
		t.Fatal("the terminal in the project was not told")
	}
	select {
	case c := <-a:
		t.Fatalf("offered twice: %+v", c)
	case c := <-b:
		t.Fatalf("a terminal in another project was told: %+v", c)
	default:
	}
	h.Hold(here.ID, []string{"s9"})
	pub(false, "gateway")
	select {
	case c := <-a:
		t.Fatalf("offered a session it already holds: %+v", c)
	default:
	}
}
