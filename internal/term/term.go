// Package term runs interactive terminals for the web admin: a shell in a
// pseudo-console, in a project's folder — on the host, or in the WSL
// distribution a project lives in — whose output is streamed to the page and
// whose input comes back from it.
//
// A terminal belongs to the gateway, not to the page: reloading the page or
// opening it in another tab reattaches to the same shell, with what it has
// printed lately replayed, the way a tmux session survives its client.
package term

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"
)

// replayBytes is how much of a terminal's output is kept for a page that
// attaches later.
const replayBytes = 256 << 10

// pty is a running pseudo-console with a process in it.
type pty interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Resize(cols, rows int) error
	// Wait blocks until the process exits and returns its exit code.
	Wait() (int, error)
	Close() error
}

// Spec says what to run, where.
type Spec struct {
	Root  string // the project, as the admin names it
	Dir   string // where the shell starts, in the shell's own namespace
	Shell string // the shell's name, for the page
	Argv  []string
	Env   []string
	Cols  int
	Rows  int
}

// Term is one terminal.
type Term struct {
	ID      string    `json:"id"`
	Root    string    `json:"root"`
	Shell   string    `json:"shell"`
	Started time.Time `json:"started"`
	Exited  bool      `json:"exited"`
	Code    int       `json:"code"`

	p    pty
	mu   sync.Mutex
	buf  []byte // the newest replayBytes of output
	subs map[int]chan []byte
	next int
	done chan struct{}
}

// Manager keeps the terminals.
type Manager struct {
	mu    sync.Mutex
	terms map[string]*Term
}

// NewManager makes an empty manager.
func NewManager() *Manager { return &Manager{terms: map[string]*Term{}} }

// Start runs a shell.
func (m *Manager) Start(s Spec) (*Term, error) {
	if len(s.Argv) == 0 {
		return nil, errors.New("nothing to run")
	}
	if s.Cols <= 0 {
		s.Cols = 100
	}
	if s.Rows <= 0 {
		s.Rows = 30
	}
	p, err := start(s)
	if err != nil {
		return nil, err
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	t := &Term{ID: hex.EncodeToString(b), Root: s.Root, Shell: s.Shell, Started: time.Now().UTC(),
		p: p, subs: map[int]chan []byte{}, done: make(chan struct{})}
	m.mu.Lock()
	m.terms[t.ID] = t
	m.mu.Unlock()
	go t.pump()
	go func() {
		code, _ := p.Wait()
		t.mu.Lock()
		t.Exited, t.Code = true, code
		t.mu.Unlock()
		// The console is closed on its own goroutine: closing it waits for
		// its host to flush, which can take a while with nobody reading.
		go func() { _ = p.Close() }()
	}()
	return t, nil
}

func (t *Term) pump() {
	defer close(t.done)
	buf := make([]byte, 32<<10)
	for {
		n, err := t.p.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			t.mu.Lock()
			t.buf = append(t.buf, chunk...)
			if len(t.buf) > replayBytes {
				t.buf = append([]byte(nil), t.buf[len(t.buf)-replayBytes:]...)
			}
			for _, c := range t.subs {
				select {
				case c <- chunk:
				default: // a page that cannot keep up misses output, not the shell
				}
			}
			t.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// Attach returns what the terminal has printed lately, a channel of what it
// prints next, which closes when the terminal ends, and a detach function.
func (t *Term) Attach() ([]byte, <-chan []byte, func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	replay := append([]byte(nil), t.buf...)
	id := t.next
	t.next++
	c := make(chan []byte, 256)
	t.subs[id] = c
	go func() {
		<-t.done
		t.mu.Lock()
		if _, ok := t.subs[id]; ok {
			delete(t.subs, id)
			close(c)
		}
		t.mu.Unlock()
	}()
	return replay, c, func() {
		t.mu.Lock()
		if _, ok := t.subs[id]; ok {
			delete(t.subs, id)
			close(c)
		}
		t.mu.Unlock()
	}
}

// Done closes when the terminal has ended and its output is drained.
func (t *Term) Done() <-chan struct{} { return t.done }

// Write sends input to the shell.
func (t *Term) Write(p []byte) error {
	_, err := t.p.Write(p)
	return err
}

// Resize tells the shell its window changed size.
func (t *Term) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	return t.p.Resize(cols, rows)
}

// State is the terminal's exit state.
func (t *Term) State() (exited bool, code int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.Exited, t.Code
}

// Get finds a terminal.
func (m *Manager) Get(id string) (*Term, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.terms[id]
	return t, ok
}

// List returns the terminals, oldest first, those of root only when root is
// given.
func (m *Manager) List(root string) []*Term {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*Term
	for _, t := range m.terms {
		if root == "" || t.Root == root {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.Before(out[j].Started) })
	return out
}

// Close ends a terminal and forgets it.
func (m *Manager) Close(id string) {
	m.mu.Lock()
	t := m.terms[id]
	delete(m.terms, id)
	m.mu.Unlock()
	if t != nil {
		go func() { _ = t.p.Close() }()
	}
}

// CloseAll ends every terminal, for shutdown.
func (m *Manager) CloseAll() {
	for _, t := range m.List("") {
		m.Close(t.ID)
	}
}
