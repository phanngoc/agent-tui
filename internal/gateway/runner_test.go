package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/config"
	"github.com/phanngoc/agent-tui/internal/engine"
	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// script is an engine that plays back a fixed turn.
type script struct{ events []agent.Event }

func (s *script) ID() string      { return "api" }
func (s *script) Label() string   { return "script" }
func (s *script) Detail() string  { return "" }
func (s *script) Available() bool { return true }
func (s *script) CanAsk() bool    { return true }
func (s *script) Run(ctx context.Context, t agent.Turn, out chan<- agent.Event) {
	defer close(out)
	for _, e := range s.events {
		if a, ok := e.(agent.EvApproval); ok {
			out <- a
			<-a.Reply
			continue
		}
		out <- e
	}
}

func TestRunnerRecordsToolResultsAndRoutesApprovals(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	call := session.ToolCall{ID: "c1", Name: "bash", Input: json.RawMessage(`{"command":"ls"}`)}
	eng := &script{events: []agent.Event{
		agent.EvAssistant{Message: session.Message{Role: session.RoleAssistant, Tools: []session.ToolCall{call}}},
		agent.EvApproval{Call: call, Reason: "ask mode", Reply: make(chan agent.Verdict, 1)},
		agent.EvToolDone{Call: session.ToolCall{ID: "c1", Result: "README.md", Done: true}},
		agent.EvAssistant{Message: session.Message{Role: session.RoleAssistant, Text: "one file"}},
		agent.EvDone{},
	}}

	h := NewHub()
	r := NewRunner(h, config.Default())
	h.Local = r
	r.roots[root] = &project{root: root, fs: vfs.NewLocal(root), dir: root, mgr: session.NewManager(config.DataDir(), root, "m"), reg: engine.NewRegistryWith(eng)}
	_, events, cancel := h.Subscribe(0)
	defer cancel()

	s, err := r.NewSession(root, "", "", "api", "", "ask", "list the files")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(10 * time.Second)
	for done := false; !done; {
		select {
		case e := <-events:
			switch e.Type {
			case EvApprovalRequest:
				var d ApprovalData
				_ = json.Unmarshal(e.Data, &d)
				if _, err := h.Route(Command{Type: CmdApprove, Session: s.ID, ID: d.ID, Verdict: "allow", From: "web"}); err != nil {
					t.Fatal(err)
				}
			case EvTurnDone:
				done = true
			}
		case <-deadline:
			t.Fatal("the turn never finished")
		}
	}
	r.Shutdown()
	got, err := Load(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 3 || got.Messages[1].Tools[0].Result != "README.md" || !got.Messages[1].Tools[0].Done {
		t.Fatalf("messages: %+v", got.Messages)
	}
	if got.Messages[1].Tools[0].Name != "bash" {
		t.Fatal("the call's name was blanked by its result")
	}
	if h.Busy(s.ID) {
		t.Fatal("still busy after the turn")
	}
}

func TestWSLPath(t *testing.T) {
	for in, want := range map[string][2]string{
		`\\wsl.localhost\Ubuntu-24.04\home\me\x`: {"Ubuntu-24.04", "/home/me/x"},
		`//wsl$/Debian/srv`:                      {"Debian", "/srv"},
		`\\wsl.localhost\Ubuntu`:                 {"Ubuntu", "/"},
	} {
		d, l, ok := WSLPath(in)
		if !ok || d != want[0] || l != want[1] {
			t.Errorf("WSLPath(%q) = %q %q %v", in, d, l, ok)
		}
	}
	if _, _, ok := WSLPath(`C:\code\x`); ok {
		t.Error("a host path read as WSL")
	}
}

// A session the gateway holds takes a new model and mode from the web, for
// its next turn; one started for a scheduled job says so, and is not handed
// to terminals as a conversation to pick up.
func TestRunnerSettingsAndJobSessions(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	eng := &script{events: []agent.Event{agent.EvAssistant{Message: session.Message{Role: session.RoleAssistant, Text: "ok"}}, agent.EvDone{}}}
	h := NewHub()
	r := NewRunner(h, config.Default())
	h.Local = r
	r.roots[root] = &project{root: root, fs: vfs.NewLocal(root), dir: root, mgr: session.NewManager(config.DataDir(), root, "m"), reg: engine.NewRegistryWith(eng)}
	_, events, cancel := h.Subscribe(0)
	defer cancel()

	s, err := r.NewJobSession("job1", root, "api", "claude-sonnet-5-5", "auto", "[scheduled: x · scheduled · now]\ncheck")
	if err != nil {
		t.Fatal(err)
	}
	for done := false; !done; {
		select {
		case e := <-events:
			done = e.Type == EvTurnDone
		case <-time.After(10 * time.Second):
			t.Fatal("the turn never finished")
		}
	}
	if owner, err := h.Route(Command{Type: CmdSettings, Session: s.ID, Model: "claude-opus-5-5", Mode: "plan", From: "web"}); err != nil || owner != h.ID {
		t.Fatalf("routed to %q: %v", owner, err)
	}
	r.Shutdown()
	got, err := Load(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "claude-opus-5-5" || got.Mode != "plan" {
		t.Fatalf("after settings: model %q mode %q", got.Model, got.Mode)
	}
	if got.Job != "job1" || SummaryOf(got).Job != "job1" {
		t.Fatalf("job = %q", got.Job)
	}
	legacy := &session.Session{Messages: []session.Message{{Role: session.RoleUser, Text: "[scheduled: old · scheduled · Mon]\nx"}}}
	if SummaryOf(legacy).Job == "" {
		t.Fatal("a run from before sessions were marked is not recognised")
	}

	// Offered to a terminal in the project: a conversation is, a run is not.
	p := h.Join("tui", root, 1)
	cmds := h.Attach(p)
	for _, job := range []string{"job1", ""} {
		sum := SummaryOf(got)
		sum.Job = job
		h.Publish(Event{Type: EvSessionUpdated, Session: got.ID, Origin: h.ID, Data: mustJSON(sum)})
	}
	select {
	case c := <-cmds:
		if c.Type != CmdOpen {
			t.Fatalf("command %+v", c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the conversation was not offered")
	}
	select {
	case c := <-cmds:
		t.Fatalf("offered twice — the run too: %+v", c)
	case <-time.After(200 * time.Millisecond):
	}
}
