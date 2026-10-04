package gateway

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/config"
	"github.com/phanngoc/agent-tui/internal/engine"
	"github.com/phanngoc/agent-tui/internal/fsx"
	"github.com/phanngoc/agent-tui/internal/kit"
	"github.com/phanngoc/agent-tui/internal/learn"
	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/task"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// Runner runs the turns of sessions no terminal holds. It is assembled from
// the same parts as the terminal app — one engine registry, executor and
// session manager per project — and applies engine events to a session the
// way the terminal does, so a conversation continued here and one continued
// there end up the same on disk.
type Runner struct {
	Hub *Hub
	Cfg config.Config
	// Learner, when set, learns from the turns run here.
	Learner *learn.Learner

	mu    sync.Mutex
	roots map[string]*project
	turns map[string]*turn
}

type project struct {
	root string
	mgr  *session.Manager
	reg  *engine.Registry
}

type turn struct {
	s         *session.Session
	cancel    context.CancelFunc
	approvals map[string]chan agent.Verdict
	choices   map[string]chan int
}

// NewRunner makes a runner publishing to hub.
func NewRunner(h *Hub, cfg config.Config) *Runner {
	return &Runner{Hub: h, Cfg: cfg, roots: map[string]*project{}, turns: map[string]*turn{}}
}

func (r *Runner) project(root string) *project {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.roots[root]; ok {
		return p
	}
	fs := vfs.NewLocal(root)
	exec := &agent.Executor{
		FS:       fs,
		Tasks:    task.NewRegistry(),
		Root:     root,
		Index:    fsx.NewIndex(fs, root, r.Cfg.IndexLimit),
		MaxBytes: int64(r.Cfg.MaxFileKB) << 10,
		Workers:  r.Cfg.Workers,
	}
	ag := agent.New(os.Getenv("ANTHROPIC_API_KEY"), exec, r.Cfg.Model, r.Cfg.Effort, r.Cfg.MaxTokens)
	p := &project{root: root, mgr: session.NewManager(config.DataDir(), root, r.Cfg.Model),
		reg: engine.NewRegistry(engine.NewAPI(ag, r.Cfg.Model), root)}
	r.roots[root] = p
	return p
}

// Engines lists the engines usable for a project.
func (r *Runner) Engines(root string) []map[string]any {
	p := r.project(root)
	var out []map[string]any
	for _, e := range p.reg.All() {
		out = append(out, map[string]any{"id": e.ID(), "label": e.Label(), "detail": e.Detail(),
			"available": e.Available(), "can_ask": e.CanAsk()})
	}
	return out
}

// Load reads a session from disk.
func Load(id string) (*session.Session, error) {
	b, err := os.ReadFile(filepath.Join(config.DataDir(), "sessions", id+".json"))
	if err != nil {
		return nil, session.ErrNoSession
	}
	var s session.Session
	if err := json.Unmarshal(b, &s); err != nil || s.ID == "" {
		return nil, session.ErrNoSession
	}
	return &s, nil
}

// NewSession creates a session in a project and runs its first prompt.
func (r *Runner) NewSession(root, engineID, model, mode, prompt string) (*session.Session, error) {
	if !config.IsDir(root) {
		return nil, fmt.Errorf("%s is not a folder", root)
	}
	p := r.project(root)
	prefs := config.LoadPrefs()
	ps := config.LoadProjectSettings(root)
	s := p.mgr.New()
	s.Engine = cmp.Or(engineID, ps.Engine, prefs.Engine, r.Cfg.Engine, "api")
	s.Model = cmp.Or(model, ps.Model, prefs.Model, r.Cfg.Model)
	s.Mode = cmp.Or(mode, ps.Mode, prefs.Mode, r.Cfg.Mode)
	if !p.reg.Has(s.Engine) {
		s.Engine = p.reg.Default().ID()
	}
	return s, r.start(p, s, prompt)
}

// Handle runs a command for a session the gateway owns.
func (r *Runner) Handle(cmd Command) error {
	switch cmd.Type {
	case CmdPrompt:
		r.mu.Lock()
		_, busy := r.turns[cmd.Session]
		r.mu.Unlock()
		if busy {
			return errors.New("this session is already running a turn")
		}
		s, err := Load(cmd.Session)
		if err != nil {
			return err
		}
		if s.Target != "" && s.Target != "host" {
			return fmt.Errorf("this session runs in %s; continue it in the terminal app", s.Target)
		}
		return r.start(r.project(s.Root), s, cmd.Text)
	case CmdCancel:
		r.mu.Lock()
		t := r.turns[cmd.Session]
		r.mu.Unlock()
		if t == nil {
			return errors.New("nothing is running")
		}
		t.cancel()
		return nil
	case CmdApprove, CmdChoose:
		r.mu.Lock()
		t := r.turns[cmd.Session]
		var (
			ach chan agent.Verdict
			cch chan int
		)
		if t != nil {
			ach, cch = t.approvals[cmd.ID], t.choices[cmd.ID]
			delete(t.approvals, cmd.ID)
			delete(t.choices, cmd.ID)
		}
		r.mu.Unlock()
		switch {
		case ach != nil:
			ach <- VerdictOf(cmd.Verdict)
			r.Hub.Publish(New(EvApprovalDone, cmd.Session, ResolvedData{ID: cmd.ID, Verdict: cmd.Verdict, By: cmd.From}))
		case cch != nil:
			cch <- cmd.Index
			r.Hub.Publish(New(EvChoiceDone, cmd.Session, ResolvedData{ID: cmd.ID, Index: cmd.Index, By: cmd.From}))
		default:
			return errors.New("that question has already been answered")
		}
		return nil
	case CmdReload:
		return nil
	}
	return fmt.Errorf("unknown command %q", cmd.Type)
}

func (r *Runner) start(p *project, s *session.Session, prompt string) error {
	eng := p.reg.Get(s.Engine)
	if eng == nil || !eng.Available() {
		eng = p.reg.Default()
	}
	if eng == nil {
		return errors.New("no engine is available")
	}
	s.Engine = eng.ID()
	s.Append(session.Message{Role: session.RoleUser, Text: prompt, At: time.Now()})
	p.mgr.Save(s)

	brief := ""
	if end := len(s.Messages) - 1; end > 0 {
		if seen := s.StateFor(eng.ID()).Seen; seen < end {
			brief = session.Brief(s.Messages[seen:end], session.BriefLimit)
		}
	}
	model := cmp.Or(s.Model, r.Cfg.Model, agent.DefaultModel)
	t := agent.Turn{
		Prompt:     prompt,
		Brief:      brief,
		History:    append([]session.Message(nil), s.Messages...),
		ExternalID: s.StateFor(eng.ID()).ExternalID,
		Fork:       s.ForkPending,
		Root:       cmp.Or(s.CWD, s.Root),
		Mode:       agent.ParseMode(s.Mode),
		Model:      model,
		FS:         vfs.NewLocal(s.Root),
	}
	hook := kit.Hook(s.Root, s.ID, eng.ID(), prompt)
	effort := cmp.Or(config.LoadProjectSettings(s.Root).Effort, config.LoadPrefs().Effort)
	t.Extras = func(ctx context.Context) agent.Extras {
		x := hook(ctx)
		x.Effort = effort // project › global; the agent's own is config.json's
		return x
	}
	if setter, ok := eng.(interface{ SetFS(vfs.FS) }); ok {
		setter.SetFS(t.FS)
	}
	s.ForkPending = false
	ctx, cancel := context.WithCancel(context.Background())
	tr := &turn{s: s, cancel: cancel, approvals: map[string]chan agent.Verdict{}, choices: map[string]chan int{}}
	r.mu.Lock()
	r.turns[s.ID] = tr
	r.mu.Unlock()

	r.Hub.Publish(Event{Type: EvTurnStarted, Session: s.ID, Root: s.Root,
		Data: mustJSON(TurnData{Prompt: prompt, Engine: eng.ID()})})
	r.Hub.Publish(Event{Type: EvMessage, Session: s.ID, Root: s.Root,
		Data: mustJSON(MessageData{Index: len(s.Messages) - 1, Message: s.Messages[len(s.Messages)-1]})})
	r.publishSummary(s, true)

	ch := make(chan agent.Event, 64)
	go eng.Run(ctx, t, ch)
	go r.pump(p, tr, eng.ID(), ch)
	return nil
}

func (r *Runner) publishSummary(s *session.Session, busy bool) {
	sum := SummaryOf(s)
	sum.Busy = busy
	sum.Owner = r.Hub.ID
	r.Hub.Publish(Event{Type: EvSessionUpdated, Session: s.ID, Root: s.Root, Data: mustJSON(sum)})
}

// pump applies a turn's events to its session, the terminal's way, and
// publishes them.
func (r *Runner) pump(p *project, tr *turn, engID string, ch <-chan agent.Event) {
	s := tr.s
	defer func() {
		r.mu.Lock()
		delete(r.turns, s.ID)
		r.mu.Unlock()
		tr.cancel()
	}()
	for ev := range ch {
		switch e := ev.(type) {
		case agent.EvAssistant:
			s.Append(e.Message)
			p.mgr.Save(s)
			r.Hub.Publish(Event{Type: EvMessage, Session: s.ID, Root: s.Root,
				Data: mustJSON(MessageData{Index: len(s.Messages) - 1, Message: e.Message})})
			continue
		case agent.EvSession:
			s.SetExternalID(engID, e.ExternalID)
			continue
		case agent.EvToolDone:
			// The result belongs on the message that made the call, as the
			// terminal records it; the event alone would leave it blank on disk.
			if i := s.MarkTool(e.Call); i >= 0 {
				p.mgr.Save(s)
				r.Hub.Publish(Event{Type: EvMessage, Session: s.ID, Root: s.Root,
					Data: mustJSON(MessageData{Index: i, Message: s.Messages[i]})})
			}
		case agent.EvUsage:
			s.InputTokens += e.In
			s.OutputTokens += e.Out
			s.CacheReads += e.CacheRead
		case agent.EvApproval:
			id := randID()
			r.mu.Lock()
			tr.approvals[id] = e.Reply
			r.mu.Unlock()
			r.Hub.Publish(Event{Type: EvApprovalRequest, Session: s.ID, Root: s.Root,
				Data: mustJSON(ApprovalData{ID: id, Call: e.Call, Reason: e.Reason})})
			continue
		case agent.EvChoice:
			id := randID()
			r.mu.Lock()
			tr.choices[id] = e.Reply
			r.mu.Unlock()
			r.Hub.Publish(Event{Type: EvChoiceRequest, Session: s.ID, Root: s.Root,
				Data: mustJSON(ChoiceData{ID: id, Call: e.Call, Question: e.Question, Options: e.Options})})
			continue
		case agent.EvDone:
			s.SetSeen(engID, len(s.Messages))
			if e.Err != nil && !errors.Is(e.Err, context.Canceled) {
				s.LastErr = e.Err.Error()
			}
			p.mgr.Save(s)
			if out := FromAgent(s.ID, ev); out != nil {
				out.Root = s.Root
				r.Hub.Publish(*out)
			}
			r.publishSummary(s, false)
			if r.Learner != nil {
				r.Learner.Notify(s.Root, s.ID, s.Messages)
			}
			continue
		}
		if out := FromAgent(s.ID, ev); out != nil {
			out.Root = s.Root
			r.Hub.Publish(*out)
		}
	}
}

// Shutdown cancels running turns and flushes saves.
func (r *Runner) Shutdown() {
	r.mu.Lock()
	for _, t := range r.turns {
		t.cancel()
	}
	roots := r.roots
	r.mu.Unlock()
	for _, p := range roots {
		p.mgr.Shutdown()
	}
}
