package kit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The browser tools come and go with the extension's connection, and turn
// what the browser sends back into text the agent reads.
func TestBrowserToolsFollowTheConnection(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	connected := false
	var asked []string
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/api/browser/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"connected": connected})
		case "/api/browser/do":
			var in struct {
				Action string         `json:"action"`
				Args   map[string]any `json:"args"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			asked = append(asked, in.Action)
			switch in.Action {
			case "snapshot":
				_ = json.NewEncoder(w).Encode(map[string]any{"tab": 3, "title": "Example", "url": "https://example.com/",
					"elements": "[1] link \"More information\" → https://iana.org", "text": "Example Domain", "scroll": map[string]any{"y": 0, "height": 900, "viewport": 700}})
			case "screenshot":
				_ = json.NewEncoder(w).Encode(map[string]any{"tab": 3, "title": "Example", "url": "https://example.com/", "path": `C:\Users\me\shots\shot-1.png`})
			default:
				w.WriteHeader(http.StatusBadGateway)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "tab 9 is not shared with agent-tui"})
			}
		}
	}))
	defer gw.Close()
	GatewayAddr = strings.TrimPrefix(gw.URL, "http://")
	defer func() { GatewayAddr = "" }()

	names := func(k *Kit) (map[string]func(context.Context, json.RawMessage) (string, bool), string) {
		x, _ := k.Extras(context.Background(), "api", "")
		m := map[string]func(context.Context, json.RawMessage) (string, bool){}
		for _, e := range x.Tools {
			m[e.Name] = e.Run
		}
		return m, x.System
	}
	tools, sys := names(For(t.TempDir()))
	if tools["browser_snapshot"] != nil || strings.Contains(sys, "<browser>") {
		t.Fatal("browser tools offered with Chrome not connected")
	}

	connected = true
	tools, sys = names(For(`\\wsl.localhost\Ubuntu-24.04\home\me\proj`))
	if tools["browser_snapshot"] == nil || tools["browser_click"] == nil || !strings.Contains(sys, "<browser>") {
		t.Fatalf("browser tools missing while connected: %s", sys)
	}
	out, isErr := tools["browser_snapshot"](context.Background(), json.RawMessage(`{}`))
	if isErr || !strings.Contains(out, "## Elements\n[1] link \"More information\"") || !strings.Contains(out, "## Page text\nExample Domain") || !strings.Contains(out, "https://example.com/") {
		t.Fatalf("snapshot: %q", out)
	}
	out, _ = tools["browser_screenshot"](context.Background(), json.RawMessage(`{}`))
	if !strings.Contains(out, "/mnt/c/Users/me/shots/shot-1.png") {
		t.Fatalf("a WSL project's agent is given the /mnt/c path: %q", out)
	}
	out, isErr = tools["browser_click"](context.Background(), json.RawMessage(`{"tab":9,"ref":1}`))
	if !isErr || !strings.Contains(out, "not shared") {
		t.Fatalf("an error from the browser: %q %v", out, isErr)
	}
	if strings.Join(asked, ",") != "snapshot,screenshot,click" {
		t.Fatalf("asked: %v", asked)
	}
}
