package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A fake stdio server: the test binary re-run with MCP_FAKE set.
func TestMain(m *testing.M) {
	if os.Getenv("MCP_FAKE") == "1" {
		fakeServer()
		return
	}
	os.Exit(m.Run())
}

func answer(id any, method string, params json.RawMessage) any {
	switch method {
	case "initialize":
		return map[string]any{"protocolVersion": protocolVersion, "serverInfo": map[string]any{"name": "fake", "version": "1.0"}}
	case "tools/list":
		var p struct{ Cursor string }
		_ = json.Unmarshal(params, &p)
		if p.Cursor == "" {
			return map[string]any{"tools": []any{map[string]any{"name": "echo", "description": "echoes",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}}}},
				"nextCursor": "2"}
		}
		return map[string]any{"tools": []any{map[string]any{"name": "fail.hard"}}}
	case "tools/call":
		var p struct {
			Name      string
			Arguments map[string]any
		}
		_ = json.Unmarshal(params, &p)
		if p.Name == "fail.hard" {
			return map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "boom"}}}
		}
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": fmt.Sprint("echo: ", p.Arguments["text"])}}}
	}
	return nil
}

func fakeServer() {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var req struct {
			ID     any             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil || req.ID == nil {
			continue
		}
		// A notification first, which the client must skip.
		fmt.Println(`{"jsonrpc":"2.0","method":"notifications/message","params":{}}`)
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": answer(req.ID, req.Method, req.Params)})
		fmt.Println(string(b))
	}
}

func fakeStdio(t *testing.T) Server {
	t.Setenv("MCP_FAKE", "1")
	return Server{Name: "fake", Command: os.Args[0], Args: []string{"-test.run=^$"}}
}

func TestStdioClientListsAndCalls(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := Connect(ctx, fakeStdio(t))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Info.Name != "fake" {
		t.Fatalf("server info %+v", c.Info)
	}
	tools, err := c.Tools(ctx)
	if err != nil || len(tools) != 2 {
		t.Fatalf("tools %v %v", tools, err)
	}
	out, isErr, err := c.CallTool(ctx, "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil || isErr || out != "echo: hi" {
		t.Fatalf("call: %q %v %v", out, isErr, err)
	}
}

func TestPoolNamesToolsForTheAPI(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	p := NewPool()
	defer p.Close()
	servers := []Server{fakeStdio(t), {Name: "dead", Command: "no-such-binary-anywhere"}}
	refs := p.Tools(ctx, servers)
	if len(refs) != 2 || refs[1].QualifiedName() != "mcp__fake__fail_hard" {
		t.Fatalf("refs: %+v", refs)
	}
	out, isErr := p.CallOn(ctx, servers, "fake", "fail.hard", nil)
	if !isErr || out != "boom" {
		t.Fatalf("call: %q %v", out, isErr)
	}
	st := p.Statuses(ctx, servers)
	if !st[0].Connected || st[1].Connected || st[1].Error == "" {
		t.Fatalf("statuses: %+v", st)
	}
}

func TestHTTPClientReadsSSEAnswers(t *testing.T) {
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
		if req.Method != "initialize" && r.Header.Get("Mcp-Session-Id") != "s1" {
			http.Error(w, "no session", http.StatusBadRequest)
			return
		}
		w.Header().Set("Mcp-Session-Id", "s1")
		w.Header().Set("Content-Type", "text/event-stream")
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": answer(req.ID, req.Method, req.Params)})
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", b)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Connect(ctx, Server{Name: "web", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := c.CallTool(ctx, "echo", json.RawMessage(`{"text":"over http"}`))
	if err != nil || out != "echo: over http" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestStoreScopesAndClaudeConfig(t *testing.T) {
	dir := t.TempDir()
	st := Store{GlobalPath: filepath.Join(dir, "g.json"), ProjectPath: filepath.Join(dir, "p", "mcp.json")}
	if err := st.Save(Server{Name: "bad name", Command: "x", Scope: Global}); err == nil {
		t.Fatal("bad name accepted")
	}
	_ = st.Save(Server{Name: "docs", URL: "http://x", Scope: Global})
	_ = st.Save(Server{Name: "docs", Command: "docs-mcp", Args: []string{"--stdio"}, Scope: Project})
	_ = st.Save(Server{Name: "off", Command: "x", Disabled: true, Scope: Global})
	act := st.Active(nil)
	if len(act) != 1 || act[0].Scope != Project || act[0].Transport() != "stdio" {
		t.Fatalf("active: %+v", act)
	}
	cfg, _ := json.Marshal(ClaudeConfig(act))
	if !strings.Contains(string(cfg), `"command":"docs-mcp"`) {
		t.Fatalf("claude config: %s", cfg)
	}
	b, _ := os.ReadFile(st.GlobalPath)
	if !strings.Contains(string(b), `"mcpServers"`) || strings.Contains(string(b), `"scope"`) {
		t.Fatalf("file: %s", b)
	}
	if err := st.Delete(Project, "docs"); err != nil || st.Active(nil)[0].Scope != Global {
		t.Fatalf("delete: %v", err)
	}
}
