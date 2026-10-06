package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/session"
)

type buf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *buf) Write(p []byte) (int, error) { b.mu.Lock(); defer b.mu.Unlock(); return b.b.Write(p) }
func (b *buf) Close() error                { return nil }
func (b *buf) String() string              { b.mu.Lock(); defer b.mu.Unlock(); return b.b.String() }

// The prompt goes in as a stream-json user message; a queued message goes in
// only while a tool runs, and nothing after the turn's result.
func TestStreamInputSteersOnlyWhileAToolRuns(t *testing.T) {
	w := &buf{}
	in := &streamInput{w: w}
	if err := in.user("build it"); err != nil {
		t.Fatal(err)
	}
	var m struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(w.String())), &m); err != nil || m.Type != "user" || m.Message.Content != "build it" {
		t.Fatalf("prompt line %q: %v", w.String(), err)
	}

	var mu sync.Mutex
	queue := []string{"use v2"}
	take := func() []string { mu.Lock(); defer mu.Unlock(); q := queue; queue = nil; return q }
	told := make(chan []string, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go in.steer(ctx, take, func(x []string) { told <- x })

	time.Sleep(700 * time.Millisecond)
	if strings.Contains(w.String(), "use v2") {
		t.Fatal("a message went in with no tool running")
	}
	in.track(agent.EvToolStart{Call: session.ToolCall{ID: "t1", Name: "Bash"}})
	select {
	case got := <-told:
		if strings.Join(got, "") != "use v2" || !strings.Contains(w.String(), `"content":"use v2"`) {
			t.Fatalf("told %q, wrote %q", got, w.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the queued message never went in while the tool ran")
	}
	in.track(agent.EvToolDone{Call: session.ToolCall{ID: "t1", Done: true}})
	in.close()
	mu.Lock()
	queue = []string{"too late"}
	mu.Unlock()
	time.Sleep(700 * time.Millisecond)
	if strings.Contains(w.String(), "too late") {
		t.Fatal("a message went in after the result")
	}
	if !isResult([]byte(`{"type":"result","result":"ok"}`)) || isResult([]byte(`{"type":"assistant","message":{"content":"result"}}`)) {
		t.Fatal("isResult")
	}
}
