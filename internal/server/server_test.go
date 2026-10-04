package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/config"
	"github.com/phanngoc/agent-tui/internal/gateway"
)

// A terminal joins as a peer; a prompt sent from the web for a session it
// holds reaches it as a command, and what it publishes reaches the web's
// event stream — the whole round trip the two views depend on.
func TestPeerRoundTrip(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	srv := New(config.Default(), "test", "")
	ts := httptest.NewServer(srv.guard(srv.mux))
	defer ts.Close()
	addr := strings.TrimPrefix(ts.URL, "http://")
	if err := gateway.WriteInfo(gateway.Info{Addr: addr, PID: 1}); err != nil {
		t.Fatal(err)
	}

	// The web side listens first.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	events := make(chan gateway.Event, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if line, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				var e gateway.Event
				if json.Unmarshal([]byte(line), &e) == nil && e.Type != "" {
					events <- e
				}
			}
		}
	}()

	c := gateway.Join("tui", "/repo", false)
	defer c.Close()
	c.Hold([]string{"s1"})
	deadline := time.Now().Add(10 * time.Second)
	for srv.Hub.Owner("s1") == "gateway" {
		if time.Now().After(deadline) {
			t.Fatal("the peer never took hold of s1")
		}
		time.Sleep(50 * time.Millisecond)
	}

	r, err := http.Post(ts.URL+"/api/sessions/s1/prompt", "application/json", strings.NewReader(`{"text":"from the web"}`))
	if err != nil || r.StatusCode != http.StatusOK {
		t.Fatalf("prompt: %v %v", err, r.Status)
	}
	select {
	case cmd := <-c.Commands():
		if cmd.Type != gateway.CmdPrompt || cmd.Text != "from the web" || cmd.From != "web" {
			t.Fatalf("command: %+v", cmd)
		}
	case <-ctx.Done():
		t.Fatal("the peer got no command")
	}

	c.Publish(gateway.New(gateway.EvTurnStarted, "s1", gateway.TurnData{Prompt: "from the web", Engine: "api"}))
	c.Publish(gateway.New(gateway.EvTextDelta, "s1", gateway.TextData{Text: "hi"}))
	var seen []string
	for len(seen) < 2 {
		select {
		case e := <-events:
			if e.Session != "s1" {
				continue // peer.joined and the like
			}
			if !strings.HasPrefix(e.Origin, "tui-") {
				t.Fatalf("origin %q", e.Origin)
			}
			seen = append(seen, e.Type)
		case <-ctx.Done():
			t.Fatalf("events seen: %v", seen)
		}
	}
	if l := srv.Hub.LiveAll()["s1"]; !l.Busy || l.Partial != "hi" {
		t.Fatalf("live: %+v", l)
	}

	// A host other than loopback is refused.
	bad, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/health", nil)
	bad.Host = "evil.example"
	if r, _ := http.DefaultClient.Do(bad); r.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign host got %d", r.StatusCode)
	}
}
