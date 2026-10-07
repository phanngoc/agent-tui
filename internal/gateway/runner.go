package gateway

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	// queues are the messages sent while a session's turn runs (queue.go).
	queues map[string][]Queued
	// closing is a shutdown under way: turns it cancels start nothing.
	closing bool
}

type project struct {
	root string
	// fs is where the work happens: the host, or a WSL distribution or
	// container the session targets; dir is the folder there.
	fs  vfs.FS
	dir string
	mgr *session.Manager
	reg *engine.Registry
}

// isHost reports whether a target means this machine.
func isHost(target string) bool { return target == "" || target == "host" }

type turn struct {
	s         *session.Session
	cancel    context.CancelFunc
	approvals map[string]chan agent.Verdict
	choices   map[string]chan int
	// steers says the engine hands queued messages to the model mid-turn.
	steers bool
}

// NewRunner makes a runner publishing to hub.
func NewRunner(h *Hub, cfg config.Config) *Runner {
	return &Runner{Hub: h, Cfg: cfg, roots: map[string]*project{}, turns: map[string]*turn{}, queues: map[string][]Queued{}}
}

// project assembles the engines for one place work happens: a host folder,
// or a folder inside a WSL distribution or container — the same vfs the
// terminal opens for such a session. Each place has its own registry, since
// a CLI engine is pointed at its filesystem.
func (r *Runner) project(root, target, cwd string) (*project, error) {
	key := root
	elsewhere := isHost(target) && cwd != "" && filepath.Clean(cwd) != filepath.Clean(root)
	switch {
	case !isHost(target):
		key += "|" + target + "|" + cwd
	case elsewhere:
		key += "|cwd|" + cwd
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.roots[key]; ok {
		return p, nil
	}
	var fs vfs.FS = vfs.NewLocal(root)
	dir := root
	if elsewhere {
		// A session working elsewhere on this machine — a git worktree of
		// the project — has its own tools there; the project (memory,
		// skills, settings) is still root.
		fs, dir = vfs.NewLocal(cwd), cwd
	}
	if !isHost(target) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		fs = vfs.Open(ctx, target, root)
		cancel()
		if fs.ID() != target {
			return nil, fmt.Errorf("%s is not reachable from here", target)
		}
		dir = cmp.Or(cwd, fs.DefaultDir())
	}
	exec := &agent.Executor{
		FS:       fs,
		Tasks:    task.NewRegistry(),
		Root:     dir,
		Index:    fsx.NewIndex(fs, dir, r.Cfg.IndexLimit),
		MaxBytes: int64(r.Cfg.MaxFileKB) << 10,
		Workers:  r.Cfg.Workers,
	}
	ag := agent.New(os.Getenv("ANTHROPIC_API_KEY"), exec, r.Cfg.Model, r.Cfg.Effort, r.Cfg.MaxTokens)
	p := &project{root: root, fs: fs, dir: dir, mgr: session.NewManager(config.DataDir(), root, r.Cfg.Model),
		reg: engine.NewRegistry(engine.NewAPI(ag, r.Cfg.Model), root)}
	r.roots[key] = p
	return p, nil
}

// WSLPath reads a Windows path into a WSL distribution — \\wsl.localhost\D\x
// or \\wsl$\D\x — as the distribution and the Linux path inside it.
func WSLPath(p string) (distro, linux string, ok bool) {
	s := strings.ReplaceAll(p, "/", `\`)
	for _, pre := range []string{`\\wsl.localhost\`, `\\wsl$\`} {
		if len(s) > len(pre) && strings.EqualFold(s[:len(pre)], pre) {
			distro, rest, _ := strings.Cut(s[len(pre):], `\`)
			if distro == "" {
				return "", "", false
			}
			return distro, "/" + strings.ReplaceAll(rest, `\`, "/"), true
		}
	}
	return "", "", false
}

// Engines lists the engines usable for a project.
func (r *Runner) Engines(root string) []map[string]any {
	p, err := r.project(root, "", "")
	if err != nil {
		return nil
	}
	var out []map[string]any
	for _, e := range p.reg.All() {
		out = append(out, map[string]any{"id": e.ID(), "label": e.Label(), "detail": e.Detail(),
			"available": e.Available(), "can_ask": e.CanAsk()})
	}
	return out
}

// Load reads a session from disk.
func Load(id string) (*session.Session, error) {
	p := filepath.Join(config.DataDir(), "sessions", id+".json")
	// A save replaces the file by renaming a new one over it; on Windows a
	// read in that instant fails though the session is there. A few tries,
	// while the file exists, ride it out.
	for try := 0; ; try++ {
		b, err := os.ReadFile(p)
		var s session.Session
		if err == nil && json.Unmarshal(b, &s) == nil && s.ID != "" {
			return &s, nil
		}
		if _, serr := os.Stat(p); serr != nil || try == 4 {
			return nil, session.ErrNoSession
		}
		time.Sleep(time.Duration(10*(try+1)) * time.Millisecond)
	}
}

// NewSession creates a session in a project and runs its first prompt.
//
// A root inside a WSL distribution (\\wsl.localhost\…) runs there, the way the
// terminal runs a session aimed at a distribution: target wsl:<name>, the
// Linux folder as its directory. target and cwd say so explicitly, for a
// location copied from an earlier session.
func (r *Runner) NewSession(root, target, cwd, engineID, model, mode, prompt string, files ...session.Attachment) (*session.Session, error) {
	return r.newSession("", "", root, target, cwd, engineID, model, mode, prompt, files...)
}

// NewJobSession is NewSession for a run of a scheduled job, marked as one.
// A title names the session for good, as the job's own; without one it is
// taken from the prompt.
func (r *Runner) NewJobSession(job, title, root, engineID, model, mode, prompt string) (*session.Session, error) {
	return r.newSession(job, title, root, "", "", engineID, model, mode, prompt)
}

func (r *Runner) newSession(job, title, root, target, cwd, engineID, model, mode, prompt string, files ...session.Attachment) (*session.Session, error) {
	// One spelling per folder: it is the project's identity for memory,
	// skills and settings.
	root = filepath.Clean(root)
	if isHost(target) {
		if d, linux, ok := WSLPath(root); ok {
			// A worktree of the project, given as cwd, is kept.
			target, cwd = "wsl:"+d, cmp.Or(cwd, linux)
		}
	}
	if !config.IsDir(root) {
		return nil, fmt.Errorf("%s is not a folder", root)
	}
	p, err := r.project(root, target, cwd)
	if err != nil {
		return nil, err
	}
	prefs := config.LoadPrefs()
	ps := config.LoadProjectSettings(root)
	s := p.mgr.New()
	s.Job, s.Title = job, title
	if !isHost(target) {
		s.Target, s.CWD = target, p.dir
	} else if cwd != "" {
		s.CWD = p.dir
	}
	s.Engine = cmp.Or(engineID, ps.Engine, prefs.Engine, r.Cfg.Engine, "api")
	s.Model = cmp.Or(model, ps.Model, prefs.Model, r.Cfg.Model)
	s.Mode = cmp.Or(mode, ps.Mode, prefs.Mode, r.Cfg.Mode)
	if !p.reg.Has(s.Engine) {
		s.Engine = p.reg.Default().ID()
	}
	return s, r.start(p, s, prompt, false, files...)
}

// Handle runs a command for a session the gateway owns.
func (r *Runner) Handle(cmd Command) error {
	switch cmd.Type {
	case CmdPrompt:
		// Sent while a turn runs: queued for it (queue.go).
		if r.enqueue(cmd.Session, cmd.Text, cmd.Now, cmd.Files...) {
			return nil
		}
		s, err := Load(cmd.Session)
		if err != nil {
			return err
		}
		p, err := r.project(s.Root, s.Target, s.CWD)
		if err != nil {
			return err
		}
		return r.start(p, s, cmd.Text, cmd.Fresh, cmd.Files...)
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
	case CmdSettings:
		return r.settings(cmd)
	}
	return fmt.Errorf("unknown command %q", cmd.Type)
}

func (r *Runner) start(p *project, s *session.Session, prompt string, fresh bool, files ...session.Attachment) error {
	eng := p.reg.Get(s.Engine)
	if eng == nil || !eng.Available() {
		eng = p.reg.Default()
	}
	if eng == nil {
		return errors.New("no engine is available")
	}
	s.Engine = eng.ID()
	if fresh {
		s.Fresh()
	}
	s.Append(session.Message{Role: session.RoleUser, Text: prompt, At: time.Now(), Files: files})
	p.mgr.SaveNow(s)

	brief := ""
	if end := len(s.Messages) - 1; end > 0 {
		if seen := s.SeenBy(eng.ID()); seen < end {
			brief = session.Brief(s.Messages[seen:end], session.BriefLimit)
		}
	}
	model := cmp.Or(s.Model, r.Cfg.Model, agent.DefaultModel)
	t := agent.Turn{
		Prompt:     prompt,
		Brief:      brief,
		History:    append([]session.Message(nil), s.Context()...),
		ExternalID: s.StateFor(eng.ID()).ExternalID,
		Fork:       s.ForkPending,
		Root:       cmp.Or(s.CWD, p.dir),
		Mode:       agent.ParseMode(s.Mode),
		Model:      model,
		FS:         p.fs,
		Files:      reach(p.fs, s.ID, files),
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
	if st, ok := eng.(agent.Steerer); ok && st.CanSteer() {
		tr.steers = true
		t.Steer = func() []string { return r.takeQueue(s.ID) }
	}
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
		// What was sent meanwhile and not yet heard goes now.
		r.next(p, s)
	}()
	for ev := range ch {
		switch e := ev.(type) {
		case agent.EvSteered:
			r.steered(p, s, e.Texts)
			continue
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
		case agent.EvSubAgent:
			if s.SetSubAgent(e.ToolUse, e.Agent) {
				p.mgr.Save(s)
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

// settings changes what a session the gateway holds runs on, from its next
// turn. A turn under way keeps its engine and model; the change is made on
// the session it is writing, so its end saves it rather than undoing it.
func (r *Runner) settings(cmd Command) error {
	r.mu.Lock()
	t := r.turns[cmd.Session]
	r.mu.Unlock()
	var s *session.Session
	if t != nil {
		s = t.s
	} else {
		var err error
		if s, err = Load(cmd.Session); err != nil {
			return err
		}
	}
	p, err := r.project(s.Root, s.Target, s.CWD)
	if err != nil {
		return err
	}
	if t == nil {
		// The project's manager may hold the session from an earlier turn,
		// and would write that copy back over this change.
		if held := p.mgr.Get(cmd.Session); held != nil {
			s = held
		}
	}
	if cmd.Engine != "" {
		e := p.reg.Get(cmd.Engine)
		if e == nil || !e.Available() {
			return fmt.Errorf("engine %s is not available here", cmd.Engine)
		}
		if s.Engine != e.ID() {
			// As the terminal hands over (ui/handoff.go): the new engine's own
			// conversation, if it had one, and no reasoning context, which
			// cannot travel.
			s.Engine = e.ID()
			s.ExternalID = s.StateFor(e.ID()).ExternalID
			s.Live = nil
		}
	}
	if cmd.Model != "" {
		s.Model = cmd.Model
	}
	if cmd.Mode != "" {
		s.Mode = agent.ParseMode(cmd.Mode).String()
	}
	// Queued behind the turn's own saves, so it is the last write and not
	// overwritten by one of them.
	p.mgr.Save(s)
	r.publishSummary(s, t != nil)
	return nil
}

// Running lists the sessions whose turns run here now: what stopping the
// gateway would cancel.
func (r *Runner) Running() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.turns))
	for id := range r.turns {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Shutdown cancels running turns and flushes saves.
func (r *Runner) Shutdown() {
	r.mu.Lock()
	r.closing = true
	for _, t := range r.turns {
		t.cancel()
	}
	roots := r.roots
	r.mu.Unlock()
	for _, p := range roots {
		p.mgr.Shutdown()
	}
}
