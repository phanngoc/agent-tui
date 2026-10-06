package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/config"
	"github.com/phanngoc/agent-tui/internal/gateway"
)

// A model chosen on the web for a session a terminal holds reaches that
// terminal as a settings command, the alias resolved to a model id; an
// unknown model is refused.
func TestSessionSettingsReachTheHolder(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	srv := New(config.Default(), "test", "")
	ts := httptest.NewServer(srv.guard(srv.mux))
	defer ts.Close()
	if err := gateway.WriteInfo(gateway.Info{Addr: strings.TrimPrefix(ts.URL, "http://"), PID: 1}); err != nil {
		t.Fatal(err)
	}
	c := gateway.Join("tui", "/repo", false)
	defer c.Close()
	c.Hold([]string{"s1"})
	for deadline := time.Now().Add(10 * time.Second); srv.Hub.Owner("s1") == srv.Hub.ID; {
		if time.Now().After(deadline) {
			t.Fatal("the peer never took hold of s1")
		}
		time.Sleep(50 * time.Millisecond)
	}
	put := func(body string) *http.Response {
		req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/sessions/s1/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := put(`{"model":"opus","mode":"plan"}`); r.StatusCode != http.StatusOK {
		t.Fatalf("settings: %s", r.Status)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case cmd := <-c.Commands():
		if cmd.Type != gateway.CmdSettings || cmd.Model != "claude-opus-5-5" || cmd.Mode != "plan" || cmd.From != "web" {
			t.Fatalf("command: %+v", cmd)
		}
	case <-ctx.Done():
		t.Fatal("the terminal got no settings command")
	}
	if r := put(`{"model":"gpt-9"}`); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("an unknown model: %s", r.Status)
	}
}
