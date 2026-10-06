package schedule

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Host is what the scheduler runs work through: the gateway.
type Host interface {
	// Start runs prompt as a turn: in a new session of the job's project,
	// or, for a job that continues one, as that session's next turn. It
	// returns the session.
	Start(j *Job, prompt string) (session string, err error)
	// Busy says a session is running a turn.
	Busy(session string) bool
	// Cancel stops a session's turn.
	Cancel(session string)
	// Result is a finished turn's last answer.
	Result(session string) string
	// Gate runs a job's gate command in its project: ok when it exits 0,
	// with what it printed.
	Gate(ctx context.Context, j *Job) (out string, ok bool, err error)
}

// Scheduler fires jobs when they are due.
type Scheduler struct {
	Store *Store
	Host  Host
	// Notify hears every finished or skipped run, for the event stream.
	Notify func(j *Job, r Run)
	// Changed hears that the jobs changed, for pages to reload them.
	Changed func()
	// MaxConcurrent is how many runs may be under way at once (default 2);
	// a run due beyond that waits its turn rather than being dropped.
	MaxConcurrent int

	now func() time.Time

	mu      sync.Mutex
	active  map[string]*active // by session
	pending map[string]string  // job → reason, for runs being prepared
	// early holds turns that ended before their run was registered — a turn
	// that fails at once can — with their errors.
	early   map[string]string
	wake    chan struct{}
	started time.Time
}

type active struct {
	job      string
	run      Run
	deadline time.Time
}

// Defaults.
const (
	tick            = 15 * time.Second
	defaultTimeout  = 30 * time.Minute
	catchUpWindow   = 7 * 24 * time.Hour
	pauseAfter      = 5   // failures in a row
	silentRest      = 300 // characters an ack may come with
	gateTimeout     = time.Minute
	checklistName   = "HEARTBEAT.md"
	maxGateOutput   = 4000
	maxStoredAnswer = 2000
)

// backoff spaces out the retries of a failing job, as OpenClaw does.
var backoff = []time.Duration{30 * time.Second, time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour}

func (s *Scheduler) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Scheduler) init() {
	if s.active == nil {
		s.active = map[string]*active{}
		s.pending = map[string]string{}
		s.early = map[string]string{}
		s.wake = make(chan struct{}, 1)
	}
	if s.MaxConcurrent <= 0 {
		s.MaxConcurrent = 2
	}
}

// Run fires jobs until ctx ends. It first settles what happened while the
// gateway was not running.
func (s *Scheduler) Run(ctx context.Context) {
	s.mu.Lock()
	s.init()
	s.started = s.clock()
	s.mu.Unlock()
	s.recover()
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		s.Tick()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.wake:
		}
	}
}

// Wake makes the scheduler look at its jobs now.
func (s *Scheduler) Wake() {
	s.mu.Lock()
	s.init()
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Scheduler) changed() {
	if s.Changed != nil {
		s.Changed()
	}
}

// recover settles the jobs after a start: a run the last gateway left
// unfinished failed with it; a job whose time passed while nothing was
// running gets one catch-up run, if it is recent enough and wanted.
func (s *Scheduler) recover() {
	now := s.clock()
	jobs, _ := s.Store.List()
	for _, j := range jobs {
		if j.State.Running != "" {
			r := Run{ID: newID(), Job: j.ID, Start: j.State.RunStarted, End: now, Status: StatusError,
				Reason: j.State.RunReason, Session: j.State.Running, Error: "the gateway stopped during the run"}
			_ = s.Store.AddRun(r)
			_, _ = s.Store.Update(j.ID, func(j *Job) bool {
				j.State.Running, j.State.LastStatus, j.State.LastError = "", StatusError, r.Error
				return true
			})
		}
		if !j.Enabled {
			continue
		}
		switch {
		case j.State.Next.IsZero():
			s.schedule(j.ID, now, false)
		case j.State.Next.Before(now) && (j.SkipMissed || now.Sub(j.State.Next) > catchUpWindow):
			missed := j.State.Next
			_ = s.Store.AddRun(Run{ID: newID(), Job: j.ID, Start: now, End: now, Status: StatusSkipped,
				Skipped: "missed at " + missed.Local().Format("2 Jan 15:04") + " while the gateway was not running"})
			s.schedule(j.ID, now, false)
		}
		// Otherwise a past Next is simply due: one catch-up run.
	}
	s.changed()
}

// schedule sets a job's next run from its schedule after t. A job with no
// next run is finished: deleted when it asked to be, else switched off.
func (s *Scheduler) schedule(id string, t time.Time, notify bool) {
	_, _ = s.Store.Update(id, func(j *Job) bool {
		next, ok := j.NextAfter(t)
		if !ok {
			if j.DeleteAfterRun {
				return false
			}
			j.Enabled, j.State.Next = false, time.Time{}
			return true
		}
		j.State.Next = next
		return true
	})
	if notify {
		s.changed()
	}
}

// Tick fires what is due now. Run calls it; tests call it directly.
func (s *Scheduler) Tick() {
	s.mu.Lock()
	s.init()
	now := s.clock()
	// Runs that outlived their time are stopped; their turn.done finishes
	// them as failed.
	for sess, a := range s.active {
		if now.After(a.deadline) {
			a.run.Error = "timed out"
			go s.Host.Cancel(sess)
			a.deadline = now.Add(time.Hour) // asked once
		}
	}
	busy := len(s.active) + len(s.pending)
	s.mu.Unlock()

	jobs, _ := s.Store.List()
	for _, j := range jobs {
		if !j.Enabled || j.State.Running != "" || j.State.Next.IsZero() || j.State.Next.After(now) {
			continue
		}
		s.mu.Lock()
		_, preparing := s.pending[j.ID]
		s.mu.Unlock()
		if preparing {
			continue
		}
		if busy >= s.MaxConcurrent {
			return // the rest wait for a slot
		}
		if j.OneSession() && j.SessionID != "" && s.Host.Busy(j.SessionID) {
			continue // after the turn under way, as /loop waits for an idle session
		}
		reason := "scheduled"
		switch {
		case j.State.RunReason == "manual":
			reason = "manual"
		case !s.started.IsZero() && j.State.Next.Before(s.started.Add(-time.Minute)):
			reason = "catch-up"
		case !j.State.Proposed.IsZero():
			reason = "paced"
		}
		busy++
		s.mu.Lock()
		s.pending[j.ID] = reason
		s.mu.Unlock()
		go s.dispatch(j, reason)
	}
}

// RunNow makes a job due at once.
func (s *Scheduler) RunNow(id string) error {
	j, ok := s.Store.Get(id)
	if !ok {
		return errors.New("no such job")
	}
	if j.State.Running != "" {
		return errors.New("it is running now")
	}
	_, err := s.Store.Update(id, func(j *Job) bool {
		j.State.Next = s.clock()
		j.State.RunReason = "manual"
		if !j.Enabled {
			// Run now on a paused job runs it once and leaves it paused.
			j.State.RunReason = "manual-once"
		}
		return true
	})
	if err != nil {
		return err
	}
	if !j.Enabled {
		go s.dispatch(j, "manual")
		return nil
	}
	s.Wake()
	return nil
}

// Saved settles a job that was just created or edited: its next run, from
// now, by its new schedule.
func (s *Scheduler) Saved(id string) {
	_, _ = s.Store.Update(id, func(j *Job) bool {
		j.State.Failures = 0
		if !j.Enabled {
			j.State.Next = time.Time{}
		}
		return true
	})
	if j, ok := s.Store.Get(id); ok && j.Enabled {
		s.schedule(id, s.clock(), false)
	}
	s.changed()
	s.Wake()
}

func (s *Scheduler) skip(j *Job, reason, why string) {
	now := s.clock()
	r := Run{ID: newID(), Job: j.ID, Start: now, End: now, Status: StatusSkipped, Reason: reason, Skipped: why}
	_ = s.Store.AddRun(r)
	_, _ = s.Store.Update(j.ID, func(j *Job) bool {
		j.State.LastRun, j.State.LastStatus, j.State.LastError, j.State.RunReason = now, StatusSkipped, why, ""
		return true
	})
	if j.Enabled {
		s.schedule(j.ID, now, false)
	}
	if s.Notify != nil {
		s.Notify(j, r)
	}
	s.changed()
}

// dispatch prepares and starts one run.
func (s *Scheduler) dispatch(j *Job, reason string) {
	defer func() {
		s.mu.Lock()
		delete(s.pending, j.ID)
		if len(s.pending) == 0 {
			clear(s.early)
		}
		s.mu.Unlock()
	}()
	var body string
	if j.Kind == KindHeartbeat {
		list := readChecklist(j.Root)
		if checklistEmpty(list) {
			s.skip(j, reason, "the checklist ("+filepath.Join(".agent-tui", checklistName)+") is empty")
			return
		}
		body = heartbeatPrompt(list)
	} else {
		body = j.Prompt
	}
	if strings.TrimSpace(j.Gate) != "" {
		ctx, cancel := context.WithTimeout(context.Background(), gateTimeout)
		out, ok, err := s.Host.Gate(ctx, j)
		cancel()
		if err != nil {
			s.skip(j, reason, "the gate could not run: "+err.Error())
			return
		}
		if !ok {
			s.skip(j, reason, "the gate said no")
			return
		}
		if out = strings.TrimSpace(out); out != "" {
			if len(out) > maxGateOutput {
				out = out[:maxGateOutput] + "\n…"
			}
			body += "\n\nWhat the gate command `" + j.Gate + "` printed:\n```\n" + out + "\n```"
		}
	}
	prompt := s.header(j, reason) + "\n" + body + s.footer(j)

	now := s.clock()
	run := Run{ID: newID(), Job: j.ID, Start: now, Reason: reason, Status: "running"}
	sess, err := s.Host.Start(j, prompt)
	if err != nil {
		run.End, run.Status, run.Error = s.clock(), StatusError, err.Error()
		s.finish(j.ID, run, "")
		return
	}
	run.Session = sess
	timeout := defaultTimeout
	if d, err := ParseDuration(j.Timeout); err == nil && j.Timeout != "" {
		timeout = d
	}
	s.mu.Lock()
	s.active[sess] = &active{job: j.ID, run: run, deadline: now.Add(timeout)}
	errText, ended := s.early[sess]
	delete(s.early, sess)
	s.mu.Unlock()
	_, _ = s.Store.Update(j.ID, func(j *Job) bool {
		j.State.Running, j.State.RunStarted = sess, now
		if j.State.RunReason != "manual-once" {
			j.State.RunReason = reason
		}
		j.State.Proposed, j.State.ProposedWhy, j.State.StopProposed = time.Time{}, "", false
		if j.OneSession() && j.SessionID != sess {
			j.SessionID = sess
		}
		return true
	})
	s.changed()
	if ended {
		s.TurnDone(sess, errText)
	}
}

func (s *Scheduler) header(j *Job, reason string) string {
	return fmt.Sprintf("[scheduled: %s · %s · %s]", j.Name, reason, s.clock().Local().Format("Mon 2 Jan 15:04"))
}

func (s *Scheduler) footer(j *Job) string {
	var b strings.Builder
	b.WriteString("\n\n(This runs on a schedule, with nobody watching. ")
	if j.Kind == KindTask {
		b.WriteString("If there is nothing worth reporting, reply NO_REPLY and nothing else. ")
	}
	if p := j.Pacing; p != nil {
		fmt.Fprintf(&b, "Before you finish, choose when to check again with the schedule tool, action next, in between %s and %s: soon while something is changing, later when it is quiet. If the work is done for good, call it with stop: true. ", p.Min, p.Max)
	}
	b.WriteString(")")
	return b.String()
}

// TurnDone hears that a session's turn ended, and its error if it failed; a
// run in it is finished.
func (s *Scheduler) TurnDone(session, errText string) {
	s.mu.Lock()
	s.init()
	a := s.active[session]
	delete(s.active, session)
	if a == nil && len(s.pending) > 0 {
		s.early[session] = errText
	}
	s.mu.Unlock()
	if a == nil {
		return
	}
	text := s.Host.Result(session)
	run := a.run
	run.End = s.clock()
	switch {
	case run.Error != "": // timed out
		run.Status = StatusError
	case errText != "":
		run.Status, run.Error = StatusError, errText
	case SilentAck(text):
		run.Status = StatusSilent
	default:
		run.Status = StatusOK
	}
	s.finish(a.job, run, text)
	s.Wake()
}

// finish records a run and sets the job's next.
func (s *Scheduler) finish(id string, run Run, text string) {
	if r := []rune(strings.TrimSpace(text)); len(r) > maxStoredAnswer {
		text = string(r[:maxStoredAnswer]) + "…"
	}
	run.Text = strings.TrimSpace(text)
	_ = s.Store.AddRun(run)
	now := s.clock()
	j, _ := s.Store.Update(id, func(j *Job) bool {
		st := &j.State
		st.Running, st.LastRun, st.LastStatus, st.LastText, st.Runs = "", now, run.Status, run.Text, st.Runs+1
		st.LastError = run.Error
		once := st.RunReason == "manual-once"
		st.RunReason = ""
		if once || !j.Enabled {
			return true // paused: it stays so
		}
		if run.Status == StatusError {
			st.Failures++
			if st.Failures >= pauseAfter {
				j.Enabled = false
				st.Next = time.Time{}
				st.LastError = fmt.Sprintf("paused after %d failures in a row: %s", st.Failures, run.Error)
				return true
			}
			st.Next = now.Add(backoff[min(st.Failures, len(backoff))-1])
			return true
		}
		st.Failures = 0
		if st.StopProposed {
			j.Enabled, st.Next = false, time.Time{}
			st.LastError = "stopped by the agent"
			return true
		}
		if p := j.Pacing; p != nil {
			lo, _ := ParseDuration(p.Min)
			hi, _ := ParseDuration(p.Max)
			wait := hi
			if !st.Proposed.IsZero() {
				wait = min(max(st.Proposed.Sub(now), lo), hi)
			}
			next := j.ActiveHours.fit(now.Add(wait))
			if !j.Until.IsZero() && next.After(j.Until) {
				j.Enabled, st.Next = false, time.Time{}
				return true
			}
			st.Next = next
			return true
		}
		next, ok := j.NextAfter(now)
		if !ok {
			if j.DeleteAfterRun {
				return false
			}
			j.Enabled, st.Next = false, time.Time{}
			return true
		}
		st.Next = next
		return true
	})
	if j == nil {
		j = &Job{ID: id}
	}
	if s.Notify != nil {
		s.Notify(j, run)
	}
	s.changed()
}

// Propose records the agent's choice of when a scheduled run's job runs
// next, or that it should stop. session is the run's.
func (s *Scheduler) Propose(session string, in time.Duration, why string, stop bool) (*Job, error) {
	s.mu.Lock()
	s.init()
	a := s.active[session]
	s.mu.Unlock()
	if a == nil {
		return nil, errors.New("this session is not a scheduled run; use action create for a new schedule")
	}
	return s.Store.Update(a.job, func(j *Job) bool {
		if stop {
			j.State.StopProposed = true
		} else {
			j.State.Proposed, j.State.ProposedWhy = s.clock().Add(in), why
		}
		return true
	})
}

// ActiveJob is the job a session's turn is a run of, if it is one.
func (s *Scheduler) ActiveJob(session string) (*Job, bool) {
	s.mu.Lock()
	s.init()
	a := s.active[session]
	s.mu.Unlock()
	if a == nil {
		return nil, false
	}
	return s.Store.Get(a.job)
}

// SilentAck says an answer is an all-is-well: HEARTBEAT_OK or NO_REPLY at
// its start or end, with little else beside it.
func SilentAck(text string) bool {
	t := strings.Trim(strings.TrimSpace(text), "*`_ .")
	for _, tok := range []string{"HEARTBEAT_OK", "NO_REPLY"} {
		var rest string
		switch {
		case strings.HasPrefix(t, tok):
			rest = strings.TrimPrefix(t, tok)
		case strings.HasSuffix(t, tok):
			rest = strings.TrimSuffix(t, tok)
		default:
			continue
		}
		return len([]rune(strings.TrimSpace(rest))) <= silentRest
	}
	return false
}

// ChecklistPath is where a project keeps its heartbeat checklist.
func ChecklistPath(root string) string { return filepath.Join(root, ".agent-tui", checklistName) }

func readChecklist(root string) string {
	b, err := os.ReadFile(ChecklistPath(root))
	if err != nil {
		return ""
	}
	return string(b)
}

// checklistEmpty says a checklist has nothing to do in it: only blank lines,
// headings, comments, fences and empty list items, as OpenClaw judges it.
func checklistEmpty(s string) bool {
	inComment := false
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if inComment {
			if strings.Contains(t, "-->") {
				inComment = false
			}
			continue
		}
		if strings.HasPrefix(t, "<!--") {
			inComment = !strings.Contains(t, "-->")
			continue
		}
		t = strings.TrimLeft(t, "-*+ ")
		t = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(t, "[ ]"), "[x]"))
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			continue
		}
		return false
	}
	return true
}

func heartbeatPrompt(list string) string {
	return "This is a heartbeat: a periodic pass over this project's checklist, below. " +
		"Work through every item that needs doing now, in this one turn. " +
		"Do not invent tasks that are not on it, and do not redo work from earlier passes unless an item says to. " +
		"If nothing needs attention, reply HEARTBEAT_OK and nothing else; otherwise say briefly what you did and what needs the user.\n\n" +
		"Checklist (.agent-tui/" + checklistName + "):\n" + strings.TrimSpace(list)
}
