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
	r.roots[root] = &project{root: root, mgr: session.NewManager(config.DataDir(), root, "m"), reg: engine.NewRegistryWith(eng)}
	_, events, cancel := h.Subscribe(0)
	defer cancel()

	s, err := r.NewSession(root, "api", "", "ask", "list the files")
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
