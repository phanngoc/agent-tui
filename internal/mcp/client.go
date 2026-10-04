package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// protocolVersion is the MCP revision this client speaks. Servers answer with
// the one they settle on, and every revision since 2024-11-05 shares the three
// calls used here.
const protocolVersion = "2025-06-18"

// Tool is one tool a server offers.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// Client is a connection to one server.
type Client struct {
	Server Server
	Info   struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}

	t     transport
	next  atomic.Int64
	tools []Tool
}

type transport interface {
	call(ctx context.Context, req []byte, id int64) (json.RawMessage, error)
	notify(ctx context.Context, req []byte) error
	close() error
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	ID     *int64          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

// Connect starts or reaches the server and completes the handshake.
func Connect(ctx context.Context, s Server) (*Client, error) {
	c := &Client{Server: s}
	var err error
	switch s.Transport() {
	case "stdio":
		c.t, err = startStdio(s)
	default:
		c.t = &httpTransport{url: s.URL, headers: s.Headers, client: &http.Client{Timeout: 2 * time.Minute}}
	}
	if err != nil {
		return nil, err
	}
	var init struct {
		ServerInfo json.RawMessage `json:"serverInfo"`
	}
	raw, err := c.Call(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "agent-tui", "version": "1"},
	})
	if err != nil {
		c.t.close()
		return nil, fmt.Errorf("%s: initialize: %w", s.Name, err)
	}
	_ = json.Unmarshal(raw, &init)
	_ = json.Unmarshal(init.ServerInfo, &c.Info)
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	_ = c.t.notify(ctx, b)
	return c, nil
}

// Call sends one request and waits for its result.
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.next.Add(1)
	msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	return c.t.call(ctx, b, id)
}

// Tools lists the server's tools, following pagination. The answer is kept:
// a server's tool list does not change within one connection often enough to
// be worth asking for on every turn.
func (c *Client) Tools(ctx context.Context) ([]Tool, error) {
	if c.tools != nil {
		return c.tools, nil
	}
	var all []Tool
	cursor := ""
	for i := 0; i < 20; i++ {
		var params any
		if cursor != "" {
			params = map[string]any{"cursor": cursor}
		}
		raw, err := c.Call(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var page struct {
			Tools      []Tool `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Tools...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if all == nil {
		all = []Tool{}
	}
	c.tools = all
	return all, nil
}

// CallTool runs a tool and flattens its content to text.
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (string, bool, error) {
	var a any = map[string]any{}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &a)
	}
	raw, err := c.Call(ctx, "tools/call", map[string]any{"name": name, "arguments": a})
	if err != nil {
		return "", true, err
	}
	var res struct {
		Content []struct {
			Type     string          `json:"type"`
			Text     string          `json:"text"`
			MimeType string          `json:"mimeType"`
			Resource json.RawMessage `json:"resource"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		IsError           bool            `json:"isError"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return string(raw), false, nil
	}
	var b strings.Builder
	for _, part := range res.Content {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		switch part.Type {
		case "text":
			b.WriteString(part.Text)
		case "resource":
			b.Write(part.Resource)
		default:
			fmt.Fprintf(&b, "[%s content, %s]", part.Type, part.MimeType)
		}
	}
	if b.Len() == 0 && len(res.StructuredContent) > 0 {
		b.Write(res.StructuredContent)
	}
	return b.String(), res.IsError, nil
}

// Close ends the connection, stopping a stdio server.
func (c *Client) Close() error { return c.t.close() }

// --- stdio ---------------------------------------------------------------

type stdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	mu     sync.Mutex // serialises writes
	wait   sync.Map   // id -> chan rpcResponse
	done   chan struct{}
	err    atomic.Value
	stderr *tailBuffer
}

func startStdio(s Server) (*stdioTransport, error) {
	cmd := exec.Command(s.Command, s.Args...)
	cmd.Env = os.Environ()
	for k, v := range s.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	hideWindow(cmd)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	t := &stdioTransport{cmd: cmd, stdin: in, done: make(chan struct{}), stderr: &tailBuffer{max: 4096}}
	cmd.Stderr = t.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", s.Command, err)
	}
	go t.read(out)
	return t, nil
}

func (t *stdioTransport) read(r io.Reader) {
	defer close(t.done)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 32<<20)
	for sc.Scan() {
		var resp rpcResponse
		if json.Unmarshal(sc.Bytes(), &resp) != nil || resp.ID == nil {
			continue // a notification or a server request: nothing waits for it
		}
		if ch, ok := t.wait.LoadAndDelete(*resp.ID); ok {
			ch.(chan rpcResponse) <- resp
		}
	}
	err := sc.Err()
	if err == nil {
		err = errors.New("server exited")
	}
	if tail := strings.TrimSpace(t.stderr.String()); tail != "" {
		err = fmt.Errorf("%w: %s", err, lastLines(tail, 6))
	}
	t.err.Store(err)
}

func (t *stdioTransport) send(b []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, err := t.stdin.Write(append(b, '\n'))
	return err
}

func (t *stdioTransport) call(ctx context.Context, req []byte, id int64) (json.RawMessage, error) {
	ch := make(chan rpcResponse, 1)
	t.wait.Store(id, ch)
	defer t.wait.Delete(id)
	if err := t.send(req); err != nil {
		return nil, err
	}
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("%s (code %d)", resp.Error.Message, resp.Error.Code)
		}
		return resp.Result, nil
	case <-t.done:
		if e, ok := t.err.Load().(error); ok {
			return nil, e
		}
		return nil, errors.New("server exited")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (t *stdioTransport) notify(_ context.Context, req []byte) error { return t.send(req) }

func (t *stdioTransport) close() error {
	_ = t.stdin.Close()
	select {
	case <-t.done:
	case <-time.After(2 * time.Second):
	}
	if t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
	_ = t.cmd.Wait()
	return nil
}

type tailBuffer struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = t.b[len(t.b)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

// --- streamable HTTP -----------------------------------------------------

type httpTransport struct {
	url     string
	headers map[string]string
	client  *http.Client
	session atomic.Value // string
}

func (t *httpTransport) post(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", protocolVersion)
	if s, _ := t.session.Load().(string); s != "" {
		req.Header.Set("Mcp-Session-Id", s)
	}
	for k, v := range t.headers {
		req.Header.Set(k, os.ExpandEnv(v))
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	if s := resp.Header.Get("Mcp-Session-Id"); s != "" {
		t.session.Store(s)
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		resp.Body.Close()
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return resp, nil
}

func (t *httpTransport) call(ctx context.Context, req []byte, id int64) (json.RawMessage, error) {
	resp, err := t.post(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	pick := func(b []byte) (json.RawMessage, bool, error) {
		var r rpcResponse
		if json.Unmarshal(b, &r) != nil || r.ID == nil || *r.ID != id {
			return nil, false, nil
		}
		if r.Error != nil {
			return nil, true, fmt.Errorf("%s (code %d)", r.Error.Message, r.Error.Code)
		}
		return r.Result, true, nil
	}

	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64<<10), 32<<20)
		var data strings.Builder
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "data:") {
				data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
				continue
			}
			if line == "" && data.Len() > 0 {
				if res, ok, err := pick([]byte(data.String())); ok {
					return res, err
				}
				data.Reset()
			}
		}
		if data.Len() > 0 {
			if res, ok, err := pick([]byte(data.String())); ok {
				return res, err
			}
		}
		return nil, errors.New("the stream ended without an answer")
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if res, ok, err := pick(b); ok {
		return res, err
	}
	return nil, fmt.Errorf("unexpected answer: %.200s", b)
}

func (t *httpTransport) notify(ctx context.Context, req []byte) error {
	resp, err := t.post(ctx, req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (t *httpTransport) close() error { return nil }
