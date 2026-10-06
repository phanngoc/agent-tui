package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sync"
	"time"

	"github.com/phanngoc/agent-tui/internal/agent"
)

// streamInput is a CLI's stdin in stream-json: the prompt, then what the
// user sends while the turn runs.
//
// A message is written only while a tool call is running. Claude Code then
// hands it to the model as soon as the calls finish, within the turn; one
// written as the model is finishing would wait for a turn of its own, after
// this process's result, and be lost with it. What is not taken here stays
// queued in the runner and goes as the next turn.
type streamInput struct {
	mu      sync.Mutex
	w       io.WriteCloser
	closed  bool
	running map[string]bool // tool calls under way
}

func (s *streamInput) user(text string) error {
	b, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}})
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	_, err := s.w.Write(append(b, '\n'))
	return err
}

func (s *streamInput) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		_ = s.w.Close()
	}
}

// track follows the tool calls under way from the decoded events.
func (s *streamInput) track(e agent.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running == nil {
		s.running = map[string]bool{}
	}
	switch v := e.(type) {
	case agent.EvToolStart:
		s.running[v.Call.ID] = true
	case agent.EvAssistant:
		for _, c := range v.Message.Tools {
			if !c.Done {
				s.running[c.ID] = true
			}
		}
	case agent.EvToolDone:
		delete(s.running, v.Call.ID)
	}
}

func (s *streamInput) busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.running) > 0 && !s.closed
}

// steer passes queued messages in while a tool runs, until the turn ends.
func (s *streamInput) steer(ctx context.Context, take func() []string, told func([]string)) {
	t := time.NewTicker(300 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return
		}
		if !s.busy() {
			continue
		}
		texts := take()
		if len(texts) == 0 {
			continue
		}
		var sent []string
		for _, x := range texts {
			if s.user(x) == nil {
				sent = append(sent, x)
			}
		}
		if len(sent) > 0 {
			told(sent)
		}
	}
}

// isResult says a stream-json line is the turn's result.
func isResult(raw []byte) bool {
	if !bytes.Contains(raw, []byte(`"result"`)) {
		return false
	}
	var probe struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(raw, &probe) == nil && probe.Type == "result"
}
