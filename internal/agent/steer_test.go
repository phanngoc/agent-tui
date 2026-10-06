package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/phanngoc/agent-tui/internal/vfs"
)

// sse writes one streamed Messages API answer.
func sse(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, e := range events {
		var probe struct{ Type string }
		_ = json.Unmarshal([]byte(e), &probe)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", probe.Type, e)
	}
}

// A message the user sends while a tool runs reaches the model in the very
// next request, beside the tool's result — not as a turn of its own.
func TestSteeringReachesTheModelBesideTheToolResult(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		n := len(bodies)
		mu.Unlock()
		start := `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"claude-sonnet-5-5","content":[],"stop_reason":null,"usage":{"input_tokens":1,"output_tokens":1}}}`
		if n == 1 {
			sse(w, start,
				`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tu1","name":"probe","input":{}}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}`,
				`{"type":"message_stop"}`)
			return
		}
		sse(w, start,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"switched to v2"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`,
			`{"type":"message_stop"}`)
	}))
	defer srv.Close()
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)

	dir := t.TempDir()
	a := New("test-key", &Executor{FS: vfs.NewLocal(dir), Root: dir}, "claude-sonnet-5-5", "", 1024)
	sent := []string{"use the v2 API instead"}
	x := Extras{
		Tools: []Extension{{
			Name: "probe",
			Def:  anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{Name: "probe", InputSchema: anthropic.ToolInputSchemaParam{Properties: map[string]any{}}}},
			Run:  func(context.Context, json.RawMessage) (string, bool) { return "probed", false },
		}},
		Steer: func() []string { out := sent; sent = nil; return out },
	}
	out := make(chan Event, 64)
	go a.RunWith(context.Background(), []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("build it"))}, ModeAuto, "", x, out)
	var steered []string
	deadline := time.After(20 * time.Second)
	for done := false; !done; {
		select {
		case ev, ok := <-out:
			if !ok {
				done = true
				break
			}
			switch e := ev.(type) {
			case EvSteered:
				steered = e.Texts
			case EvDone:
				if e.Err != nil {
					t.Fatal(e.Err)
				}
			}
		case <-deadline:
			t.Fatal("the turn never ended")
		}
	}
	if strings.Join(steered, "") != "use the v2 API instead" {
		t.Fatalf("EvSteered %q", steered)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("%d requests; want 2", len(bodies))
	}
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type      string `json:"type"`
				Text      string `json:"text"`
				ToolUseID string `json:"tool_use_id"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(bodies[1]), &req); err != nil {
		t.Fatal(err)
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != "user" || len(last.Content) != 2 || last.Content[0].Type != "tool_result" || last.Content[1].Type != "text" || last.Content[1].Text != "use the v2 API instead" {
		t.Fatalf("the last message of the second request: %+v", last)
	}
}
