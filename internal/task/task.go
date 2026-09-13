// Package task tracks commands that outlive the turn that started them.
//
// Both sources of background work land here: the built-in agent, which runs
// them in this process, and Claude Code, which reports its own through the
// event stream. Having one model means the UI shows them the same way whoever
// started them.
package task

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// State is where a task is in its life.
type State string

const (
	Running State = "running"
	Done    State = "done"
	Failed  State = "failed"
	Stopped State = "stopped"
)

// Task is one background command.
//
// Everything that moves after the task starts lives behind the mutex: the
// goroutine waiting on the process writes it while the UI is reading, and a
// field left outside the lock is a race that only shows up under load.
type Task struct {
	ID      string
	Label   string
	Command string
	Started time.Time
	// Owner is the session that started it, so the UI can scope the list.
	Owner string

	mu     sync.RWMutex
	state  State
	ended  time.Time
	exit   int
	lines  []string
	cancel context.CancelFunc
}

// State is where the task is now.
func (t *Task) State() State {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.state
}

// Exit is the process exit code, or -1 when it never ran.
func (t *Task) Exit() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.exit
}

// Ended is when the task finished, zero while it is still running.
func (t *Task) Ended() time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.ended
}

// finish records a terminal state exactly once.
func (t *Task) finish(state State, exit int) {
	t.mu.Lock()
	if t.state == Running {
		t.state, t.exit, t.ended = state, exit, time.Now()
	}
	t.mu.Unlock()
}

// maxLines is how much output a task keeps. Enough to see what happened,
// bounded so a chatty build cannot grow without limit.
const maxLines = 2000

// Output returns the captured output. The slice is replaced, never mutated, so
// callers may hold it.
func (t *Task) Output() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lines
}

// Tail returns the last n lines.
func (t *Task) Tail(n int) []string {
	out := t.Output()
	if n > 0 && len(out) > n {
		return out[len(out)-n:]
	}
	return out
}

func (t *Task) append(line string) {
	t.mu.Lock()
	next := append(t.lines[:len(t.lines):len(t.lines)], line)
	if len(next) > maxLines {
		next = next[len(next)-maxLines:]
	}
	t.lines = next
	t.mu.Unlock()
}

// Elapsed is how long the task has been running, or ran for.
func (t *Task) Elapsed() time.Duration {
	if end := t.Ended(); !end.IsZero() {
		return end.Sub(t.Started)
	}
	return time.Since(t.Started)
}

// Live reports whether the task is still going.
func (t *Task) Live() bool { return t.State() == Running }

// Stop asks a task to end. It is a no-op for tasks this process does not own.
func (t *Task) Stop() {
	t.mu.Lock()
	cancel := t.cancel
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Registry holds every known task.
type Registry struct {
	mu    sync.RWMutex
	order []string
	byID  map[string]*Task
	seq   atomic.Int64
	// changed is pinged whenever anything moves, so the UI can repaint without
	// polling every task.
	changed chan struct{}
}

func NewRegistry() *Registry {
	return &Registry{byID: map[string]*Task{}, changed: make(chan struct{}, 1)}
}

// Changed fires when a task is added, produces output, or finishes.
func (r *Registry) Changed() <-chan struct{} { return r.changed }

func (r *Registry) ping() {
	select {
	case r.changed <- struct{}{}:
	default:
	}
}

// All returns every task, newest last.
func (r *Registry) All() []*Task {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Task, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.byID[id])
	}
	return out
}

// Get looks a task up by id.
func (r *Registry) Get(id string) *Task {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byID[id]
}

// LiveCount is how many tasks are still running.
func (r *Registry) LiveCount() int {
	n := 0
	for _, t := range r.All() {
		if t.Live() {
			n++
		}
	}
	return n
}

func (r *Registry) add(t *Task) {
	r.mu.Lock()
	if _, seen := r.byID[t.ID]; !seen {
		r.order = append(r.order, t.ID)
	}
	r.byID[t.ID] = t
	r.mu.Unlock()
	r.ping()
}

// Adopt records a task started elsewhere — by an agent CLI, say — so it shows
// up beside the ones this process runs.
func (r *Registry) Adopt(id, label, owner string) *Task {
	if t := r.Get(id); t != nil {
		return t
	}
	t := &Task{
		ID: id, Label: label, Command: label,
		state: Running, Started: time.Now(), Owner: owner,
	}
	r.add(t)
	return t
}

// Update moves an adopted task along.
func (r *Registry) Update(id string, state State, note string) {
	t := r.Get(id)
	if t == nil {
		return
	}
	if note != "" {
		t.append(note)
	}
	if state != "" && state != Running {
		t.finish(state, 0)
	}
	r.ping()
}

// Start runs a command in the background and returns straight away.
//
// The command is detached from the turn that asked for it: cancelling the turn
// does not kill it, because the point of backgrounding something is that it
// keeps going while the conversation moves on.
func (r *Registry) Start(dir, label string, cmd *exec.Cmd, cancel context.CancelFunc, owner string) *Task {
	id := "t" + strconv.FormatInt(r.seq.Add(1), 36)
	t := &Task{
		ID: id, Label: label, Command: label,
		state: Running, Started: time.Now(), Owner: owner, cancel: cancel,
	}

	fail := func(err error) *Task {
		t.append(err.Error())
		t.finish(Failed, -1)
		r.add(t)
		return t
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fail(err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return fail(err)
	}
	r.add(t)

	// Wait closes the pipe, and doing that while the reader is still draining
	// truncates the output — the exact hazard os/exec documents. So the reader
	// finishes first.
	drained := make(chan struct{})
	go func() {
		r.pump(t, stdout)
		close(drained)
	}()
	go func() {
		<-drained
		err := cmd.Wait()
		if err == nil {
			t.finish(Done, 0)
		} else {
			t.append("(exit: " + err.Error() + ")")
			t.finish(Failed, exitCode(err))
		}
		r.ping()
	}()
	return t
}

func (r *Registry) pump(t *Task, out io.ReadCloser) {
	defer out.Close()
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 16<<10), 1<<20)
	for sc.Scan() {
		t.append(strings.TrimRight(sc.Text(), "\r"))
		r.ping()
	}
}

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}
