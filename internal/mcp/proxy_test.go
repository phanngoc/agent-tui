package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// proxyPipe runs Proxy for s and returns a function that sends one request
// and waits for its answer.
func proxyPipe(t *testing.T, s Server) func(method string, params any) map[string]any {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan struct{})
	go func() {
		_ = Proxy(ctx, inR, outW, s)
		outW.Close()
		close(done)
	}()
	t.Cleanup(func() {
		inW.Close()
		cancel()
		<-done
	})
	sc := bufio.NewScanner(outR)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	id := 0
	return func(method string, params any) map[string]any {
		t.Helper()
		id++
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		if _, err := inW.Write(append(b, '\n')); err != nil {
			t.Fatal(err)
		}
		if !sc.Scan() {
			t.Fatalf("%s: no answer", method)
		}
		var m map[string]any
		_ = json.Unmarshal(sc.Bytes(), &m)
		if m["id"] != float64(id) {
			t.Fatalf("%s: answer for %v", method, m["id"])
		}
		return m
	}
}

// Through the proxy a client sees the server itself: its name, its tools
// page by page, its answers as they came.
func TestProxyPassesTheServerThrough(t *testing.T) {
	call := proxyPipe(t, fakeStdio(t))
	init := call("initialize", map[string]any{"protocolVersion": "2025-06-18"})
	if v := init["result"].(map[string]any)["protocolVersion"]; v != "2025-06-18" {
		t.Fatalf("initialize: %v", init)
	}
	page := call("tools/list", nil)["result"].(map[string]any)
	if page["nextCursor"] != "2" || len(page["tools"].([]any)) != 1 {
		t.Fatalf("tools/list: %v", page)
	}
	res := call("tools/call", map[string]any{"name": "echo", "arguments": map[string]any{"text": "hi"}})
	b, _ := json.Marshal(res["result"])
	if !strings.Contains(string(b), "echo: hi") {
		t.Fatalf("tools/call: %v", res)
	}
}

// A server that does not answer the first handshake, and later forgets the
// session, is still used: the proxy tries again, and makes a new connection
// when the old one is refused.
func TestProxyRidesOutASlowAndForgetfulServer(t *testing.T) {
	prevAttempt, prevInit := proxyAttempt, proxyInitWait
	proxyAttempt, proxyInitWait = 300*time.Millisecond, 5*time.Second
	t.Cleanup(func() { proxyAttempt, proxyInitWait = prevAttempt, prevInit })

	var inits, session atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if req.Method == "initialize" {
			if inits.Add(1) == 1 {
				<-r.Context().Done() // the first handshake hangs
				return
			}
			w.Header().Set("Mcp-Session-Id", fmt.Sprint("s", session.Add(1)))
		} else if r.Header.Get("Mcp-Session-Id") != fmt.Sprint("s", session.Load()) {
			http.Error(w, "no such session", http.StatusNotFound)
			return
		}
		result := answer(req.ID, req.Method, req.Params)
		if req.Method == "initialize" {
			result = map[string]any{"protocolVersion": protocolVersion, "serverInfo": map[string]any{"name": "dd"},
				"capabilities": map[string]any{"tools": map[string]any{}}, "instructions": "load a skill first"}
		}
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	}))
	defer srv.Close()

	call := proxyPipe(t, Server{Name: "dd", Type: "http", URL: srv.URL})
	init := call("initialize", map[string]any{"protocolVersion": "2025-06-18"})["result"].(map[string]any)
	if init["instructions"] != "load a skill first" {
		t.Fatalf("the server's instructions were not passed on: %v", init)
	}
	if got := call("tools/list", nil); got["result"] == nil {
		t.Fatalf("tools/list: %v", got)
	}
	session.Add(1) // the server forgets the session
	if got := call("tools/list", nil); got["result"] == nil {
		t.Fatalf("tools/list after the session was lost: %v", got)
	}
	res := call("tools/call", map[string]any{"name": "echo", "arguments": map[string]any{"text": "x"}})
	if res["result"] == nil {
		t.Fatalf("tools/call: %v", res)
	}
	if n := inits.Load(); n != 3 {
		t.Fatalf("%d handshakes; want 3 (one hung, one, one after the session was lost)", n)
	}
}
