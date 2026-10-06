package gateway

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/config"
	"github.com/phanngoc/agent-tui/internal/engine"
	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// stepper is an engine whose turns wait to be let go, and which takes
// queued messages mid-turn when steers is set.
type stepper struct {
	steers bool
	gate   chan struct{}
	mu     sync.Mutex
	turns  []agent.Turn
}

func (s *stepper) ID() string      { return "api" }
func (s *stepper) Label() string   { return "stepper" }
func (s *stepper) Detail() string  { return "" }
func (s *stepper) Available() bool { return true }
func (s *stepper) CanAsk() bool    { return true }
func (s *stepper) CanSteer() bool  { return s.steers }
func (s *stepper) Run(ctx context.Context, t agent.Turn, out chan<- agent.Event) {
	defer close(out)
	s.mu.Lock()
	s.turns = append(s.turns, t)
	s.mu.Unlock()
	out <- agent.EvToolStart{Call: session.ToolCall{ID: "t1", Name: "bash"}}
	select {
	case <-s.gate:
	case <-ctx.Done():
		out <- agent.EvDone{Err: ctx.Err()}
		return
	}
	if t.Steer != nil {
		if more := t.Steer(); len(more) > 0 {
			out <- agent.EvSteered{Texts: more}
		}
	}
	out <- agent.EvAssistant{Message: session.Message{Role: session.RoleAssistant, Text: "done: " + t.Prompt}}
	out <- agent.EvDone{}
}

func (s *stepper) prompts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var p []string
	for _, t := range s.turns {
		p = append(p, t.Prompt)
	}
	return p
}

func stepperRunner(t *testing.T, eng *stepper) (*Runner, *Hub, string) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	h := NewHub()
	r := NewRunner(h, config.Default())
	h.Local = r
	r.roots[root] = &project{root: root, fs: vfs.NewLocal(root), dir: root, mgr: session.NewManager(config.DataDir(), root, "m"), reg: engine.NewRegistryWith(eng)}
	return r, h, root
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (r *Runner) busy(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.turns[id] != nil
}

// An engine that steers gets what was sent mid-turn within the same turn,
// and the transcript shows it where it was heard.
func TestQueuedMessageSteersTheRunningTurn(t *testing.T) {
	eng := &stepper{steers: true, gate: make(chan struct{}, 4)}
	r, h, root := stepperRunner(t, eng)
	s, err := r.NewSession(root, "", "", "api", "", "", "build it")
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "the turn never started", func() bool { return len(eng.prompts()) == 1 })
	if _, err := h.Route(Command{Type: CmdPrompt, Session: s.ID, Text: "use the v2 API instead", From: "web"}); err != nil {
		t.Fatal(err)
	}
	if q := r.Queue(s.ID); len(q.Items) != 1 || !q.Steers {
		t.Fatalf("queue %+v", q)
	}
	eng.gate <- struct{}{}
	eventually(t, "the turn never ended", func() bool { return !r.busy(s.ID) })
	time.Sleep(100 * time.Millisecond)
	if p := eng.prompts(); len(p) != 1 {
		t.Fatalf("turns %q; a steered message became a turn of its own", p)
	}
	r.Shutdown()
	got, _ := Load(s.ID)
	var roles []string
	for _, m := range got.Messages {
		roles = append(roles, m.Role+map[bool]string{true: "(steered)", false: ""}[m.Steered])
	}
	if strings.Join(roles, ",") != "user,user(steered),assistant" {
		t.Fatalf("transcript %v", roles)
	}
}

// An engine that cannot steer gets the queue as its next turn, in order,
// as one message.
func TestQueuedMessagesGoNextWithoutSteering(t *testing.T) {
	eng := &stepper{gate: make(chan struct{}, 4)}
	r, h, root := stepperRunner(t, eng)
	s, _ := r.NewSession(root, "", "", "api", "", "", "first")
	eventually(t, "no turn", func() bool { return len(eng.prompts()) == 1 })
	for _, m := range []string{"second", "third"} {
		_, _ = h.Route(Command{Type: CmdPrompt, Session: s.ID, Text: m})
	}
	if q := r.Queue(s.ID); len(q.Items) != 2 || q.Steers {
		t.Fatalf("queue %+v", q)
	}
	eng.gate <- struct{}{}
	eventually(t, "the queue never went", func() bool { return len(eng.prompts()) == 2 })
	if p := eng.prompts()[1]; p != "second\n\nthird" {
		t.Fatalf("next turn %q", p)
	}
	eng.gate <- struct{}{}
	eventually(t, "the second turn never ended", func() bool { return !r.busy(s.ID) })
	r.Shutdown()
}

// Send now stops the turn, and what was queued goes at once; a message
// taken back never goes.
func TestSendNowStopsTheTurnAndUnqueue(t *testing.T) {
	eng := &stepper{gate: make(chan struct{}, 4)}
	r, h, root := stepperRunner(t, eng)
	s, _ := r.NewSession(root, "", "", "api", "", "", "slow thing")
	eventually(t, "no turn", func() bool { return len(eng.prompts()) == 1 })
	_, _ = h.Route(Command{Type: CmdPrompt, Session: s.ID, Text: "never mind"})
	item := r.Queue(s.ID).Items[0].ID
	if text, ok := r.Unqueue(s.ID, item); !ok || text != "never mind" {
		t.Fatalf("unqueue %q %v", text, ok)
	}
	_, _ = h.Route(Command{Type: CmdPrompt, Session: s.ID, Text: "do this instead", Now: true})
	eventually(t, "send now did not start the next turn", func() bool { return len(eng.prompts()) == 2 })
	if p := eng.prompts()[1]; p != "do this instead" {
		t.Fatalf("next turn %q", p)
	}
	eng.gate <- struct{}{}
	eventually(t, "the turn never ended", func() bool { return !r.busy(s.ID) })
	r.Shutdown()
}
