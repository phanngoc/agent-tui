package agent

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/phanngoc/agent-tui/internal/session"
)

// Watching a turn happen, rather than waiting for it.
//
// Two things used to be invisible. A tool call was only shown once the whole
// assistant turn had been written, so a model spending ten seconds composing a
// file to write looked like a model doing nothing; and a command's output was
// held until it exited, so the test run you were waiting on said nothing until
// there was nothing left to wait for. Both are forwarded as they happen.

// pendingCalls accumulates the tool calls of a turn as the model writes them.
//
// The inputs are collected as raw fragments and never parsed here: they are not
// valid JSON until the block ends, and the only reader is a summary line that
// knows how to read half of one.
type pendingCalls struct {
	order []int64
	by    map[int64]*pendingCall
}

type pendingCall struct {
	id    string
	name  string
	input strings.Builder
}

func (p *pendingCalls) start(index int64, id, name string) {
	if p.by == nil {
		p.by = make(map[int64]*pendingCall, 4)
	}
	if _, seen := p.by[index]; !seen {
		p.order = append(p.order, index)
	}
	p.by[index] = &pendingCall{id: id, name: name}
}

// grow appends a fragment and reports whether anything changed, so a delta for
// a block nobody announced does not cause a redraw.
func (p *pendingCalls) grow(index int64, fragment string) bool {
	c, ok := p.by[index]
	if !ok || fragment == "" {
		return false
	}
	c.input.WriteString(fragment)
	return true
}

func (p *pendingCalls) calls() []session.ToolCall {
	out := make([]session.ToolCall, 0, len(p.order))
	for _, i := range p.order {
		c := p.by[i]
		out = append(out, session.ToolCall{
			ID:    c.id,
			Name:  c.name,
			Input: json.RawMessage(c.input.String()),
		})
	}
	return out
}

// liveOutput is the writer a running command's stdout and stderr both go to.
//
// It keeps the whole output for the model and forwards it to the watcher in
// whole lines. Partial lines are held back: a progress bar redrawing itself
// with carriage returns would otherwise arrive as a thousand events, and half a
// line of a stack trace is not worth a frame.
type liveOutput struct {
	mu    sync.Mutex
	all   bytes.Buffer // everything, which is what the model is given
	line  bytes.Buffer // the line still being written
	ready bytes.Buffer // whole lines not yet forwarded

	emit func(string)
	last time.Time
	max  int
}

// flushEvery bounds how often a talkative command can redraw the transcript.
// A build printing thousands of lines a second is still readable at this rate,
// and the update loop is left alone between them.
const flushEvery = 80 * time.Millisecond

func (w *liveOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.all.Len() < w.max {
		w.all.Write(p)
	}
	for _, b := range p {
		if b == '\r' {
			continue
		}
		w.line.WriteByte(b)
		if b == '\n' {
			w.ready.Write(w.line.Bytes())
			w.line.Reset()
		}
	}
	if w.ready.Len() > 0 && time.Since(w.last) >= flushEvery {
		w.send()
	}
	return len(p), nil
}

// Close forwards whatever is left, including a final line with no newline on
// it, which is what a prompt or a progress line looks like.
func (w *liveOutput) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.line.Len() > 0 {
		w.ready.Write(w.line.Bytes())
		w.line.Reset()
	}
	w.send()
}

// send hands over what is ready. The caller holds the lock.
func (w *liveOutput) send() {
	if w.ready.Len() == 0 {
		return
	}
	if w.emit != nil {
		w.emit(w.ready.String())
	}
	w.ready.Reset()
	w.last = time.Now()
}

func (w *liveOutput) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.all.String()
}
