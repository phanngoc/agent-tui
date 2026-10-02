package task

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Following a command that another program is running.
//
// Claude Code runs its own shell commands, and the event stream says only
// that one started and, later, that it ended. While it runs — a test suite, a
// deploy, a nine-minute build — there was nothing to look at but a spinner.
// But Claude Code writes the output to a file as it goes, foreground commands
// included, so the output can be had by reading that file, and that is all
// this does: it reads the end of it again whenever it changes, until the
// command is over.

// followEvery is how often a followed file is looked at. Often enough that a
// progress line moves while you watch it, rarely enough that a stat is all
// an idle command costs.
var followEvery = 400 * time.Millisecond

// followTail is how much of the end of the file is read. The view shows a
// screenful, and maxLines bounds what is kept; a log of a gigabyte should not
// be read from the start to show its last twenty lines.
const followTail = 256 << 10

// Patience for a file that does not turn up or that goes away. A command that
// never reported its end — the agent was killed, say — would otherwise be
// followed for as long as this process lives.
const (
	followFind = 2 * time.Minute
	followGone = 5 * time.Second
)

// Follow keeps a task's output in step with a file that another program is
// writing it to. candidates are where the file may be, most likely first; a
// candidate may be a glob pattern. Following an already followed task does
// nothing.
func (r *Registry) Follow(id string, candidates []string) {
	t := r.Get(id)
	if t == nil || len(candidates) == 0 {
		return
	}
	t.mu.Lock()
	if t.following {
		t.mu.Unlock()
		return
	}
	t.following = true
	t.notes = append([]string(nil), t.lines...)
	t.mu.Unlock()

	go r.follow(t, candidates)
}

func (r *Registry) follow(t *Task, candidates []string) {
	var (
		path    string
		size    int64 = -1
		mod     time.Time
		start   = time.Now()
		lastSaw time.Time
	)
	tick := time.NewTicker(followEvery)
	defer tick.Stop()

	for {
		// Read once more after the task has ended: the last lines are often
		// the ones that say how it went.
		live := t.Live()

		if path == "" {
			path = locate(candidates)
		}
		if path != "" {
			if st, err := os.Stat(path); err == nil {
				lastSaw = time.Now()
				if st.Size() != size || !st.ModTime().Equal(mod) {
					size, mod = st.Size(), st.ModTime()
					if lines, ok := readTail(path, followTail); ok {
						t.setFile(lines)
						r.ping()
					}
				}
			}
		}

		switch {
		case !live:
			return
		case path == "" && time.Since(start) > followFind:
			return
		case path != "" && time.Since(lastSaw) > followGone:
			// A foreground command's file is removed when it finishes. What
			// was read stays; there is nothing left to read.
			return
		}
		<-tick.C
	}
}

// locate finds the first candidate that exists.
func locate(candidates []string) string {
	for _, c := range candidates {
		if strings.ContainsAny(c, "*?[") {
			if m, _ := filepath.Glob(c); len(m) > 0 {
				return m[0]
			}
			continue
		}
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

// readTail reads up to n bytes from the end of a file, as lines.
func readTail(path string, n int64) ([]string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, false
	}
	off := max(0, st.Size()-n)
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil, false
	}
	b, err := io.ReadAll(io.LimitReader(f, n))
	if err != nil {
		return nil, false
	}
	if off > 0 {
		// Started in the middle of a line; that line is not ours to show.
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			b = b[i+1:]
		}
	}
	return splitOutput(string(b)), true
}

// splitOutput turns raw output into the lines a terminal would be showing.
//
// A progress bar redraws itself by returning the carriage, so one line in the
// file is every frame of it; what is on the screen is the last frame, and
// that is all the reader wants.
func splitOutput(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		l = strings.TrimRight(l, "\r")
		if j := strings.LastIndexByte(l, '\r'); j >= 0 {
			l = l[j+1:]
		}
		lines[i] = l
	}
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return lines
}

// setFile replaces what the file said.
func (t *Task) setFile(lines []string) {
	t.mu.Lock()
	t.file = lines
	t.joinLocked()
	t.mu.Unlock()
}

// joinLocked rebuilds lines from the file and the notes. The caller holds mu.
func (t *Task) joinLocked() {
	next := make([]string, 0, len(t.file)+len(t.notes))
	next = append(next, t.file...)
	next = append(next, t.notes...)
	if len(next) > maxLines {
		next = next[len(next)-maxLines:]
	}
	t.lines = next
}

// Following reports whether the output comes from a followed file.
func (t *Task) Following() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.following
}
