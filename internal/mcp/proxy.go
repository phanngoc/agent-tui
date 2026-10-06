package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"time"
)

// Proxy serves one server over newline-delimited JSON-RPC on r and w — stdio
// MCP — passing every request through to the server itself, until r ends.
//
// It is how a CLI in a WSL distribution reaches this machine's servers. A
// distribution runs agent-tui.exe through interop, and the proxy connects
// from here: a remote server with the sign-in kept here, renewed whenever it
// runs out, however long the run; a command-line server with its Windows
// command. Handed the URL itself, Claude Code connects once, with a token that
// lasts an hour, and gives up on the server for the whole run when that one
// connection is slow (Datadog's sometimes is: CONNECT_TIMEOUT after 30 s, and
// a scheduled run went without it).
//
// The proxy answers the client at once and connects in the background,
// trying again while it has time; a dropped connection is made again on the
// next request.
func Proxy(ctx context.Context, r io.Reader, w io.Writer, s Server) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	p := &proxy{s: s, sem: make(chan struct{}, 1), dial: Connect}
	defer p.close()
	go func() {
		// The slow part, overlapped with the client starting up.
		dctx, stop := context.WithTimeout(ctx, proxyConnectBudget)
		defer stop()
		_, _ = p.get(dctx)
	}()

	var wmu sync.Mutex
	write := func(msg map[string]any) {
		msg["jsonrpc"] = "2.0"
		b, _ := json.Marshal(msg)
		wmu.Lock()
		defer wmu.Unlock()
		_, _ = w.Write(append(b, '\n'))
	}
	var (
		inflight sync.Map // request id -> cancel
		wg       sync.WaitGroup
	)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 32<<20)
	for sc.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil {
			continue
		}
		if len(req.ID) == 0 {
			// A notification. The client giving up on a request is passed on
			// as the request's context ending; the rest are the proxy's own
			// business with the server, not the client's.
			if req.Method == "notifications/cancelled" {
				var c struct {
					RequestID json.RawMessage `json:"requestId"`
				}
				_ = json.Unmarshal(req.Params, &c)
				if f, ok := inflight.Load(string(c.RequestID)); ok {
					f.(context.CancelFunc)()
				}
			}
			continue
		}
		rctx, rcancel := context.WithCancel(ctx)
		inflight.Store(string(req.ID), rcancel)
		wg.Add(1)
		go func(id json.RawMessage, method string, params json.RawMessage) {
			defer wg.Done()
			defer inflight.Delete(string(id))
			defer rcancel()
			res, err := p.handle(rctx, method, params)
			if err != nil {
				write(map[string]any{"id": id, "error": rpcErrorFor(s.Name, err)})
				return
			}
			write(map[string]any{"id": id, "result": res})
		}(req.ID, req.Method, req.Params)
	}
	cancel()
	wg.Wait()
	return sc.Err()
}

// Variables, for the tests.
var (
	// proxyConnectBudget is how long the proxy keeps trying to reach the
	// server before a request: inside the minute Claude Code waits for an
	// answer to one.
	proxyConnectBudget = 45 * time.Second
	// proxyAttempt is one try at it. Datadog answers in one to three
	// seconds, or not at all.
	proxyAttempt = 10 * time.Second
	// proxyInitWait is how long the handshake waits for the server, to pass
	// on what it tells a model about itself; Claude Code waits 30 s for it.
	proxyInitWait = 20 * time.Second
)

type proxy struct {
	s    Server
	sem  chan struct{} // held while connecting, and while reading c
	c    *Client
	dial func(context.Context, Server) (*Client, error)
}

// get returns the connection, making it when there is none, trying again
// until ctx ends. One attempt runs at a time; the others wait for it.
func (p *proxy) get(ctx context.Context) (*Client, error) {
	select {
	case p.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-p.sem }()
	if p.c != nil {
		return p.c, nil
	}
	ctx, cancel := context.WithTimeout(ctx, proxyConnectBudget)
	defer cancel()
	wait := 500 * time.Millisecond
	for {
		actx, stop := context.WithTimeout(ctx, proxyAttempt)
		c, err := p.dial(actx, p.s)
		stop()
		if err == nil {
			p.c = c
			return c, nil
		}
		if NeedsAuth(err) {
			return nil, err // asking again will not sign anyone in
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(wait):
		}
		wait = min(wait*2, 4*time.Second)
	}
}

// drop forgets a connection that failed, for the next request to make anew.
func (p *proxy) drop(c *Client) {
	p.sem <- struct{}{}
	defer func() { <-p.sem }()
	if p.c == c {
		_ = c.Close()
		p.c = nil
	}
}

func (p *proxy) close() {
	p.sem <- struct{}{}
	defer func() { <-p.sem }()
	if p.c != nil {
		_ = p.c.Close()
		p.c = nil
	}
}

func (p *proxy) handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "initialize":
		var in struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(params, &in)
		v := in.ProtocolVersion
		if v == "" {
			v = protocolVersion
		}
		res := map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": p.s.Name, "version": "1"},
		}
		// What the server says about itself, if it is there in time; the
		// tools work without it.
		wctx, cancel := context.WithTimeout(ctx, proxyInitWait)
		defer cancel()
		if c, err := p.get(wctx); err == nil {
			if len(c.Capabilities) > 0 && string(c.Capabilities) != "null" {
				res["capabilities"] = c.Capabilities
			}
			if c.Instructions != "" {
				res["instructions"] = c.Instructions
			}
		}
		return res, nil
	case "ping":
		return map[string]any{}, nil
	}
	var prm any
	if len(params) > 0 && string(params) != "null" {
		prm = params
	}
	for retried := false; ; retried = true {
		c, err := p.get(ctx)
		if err != nil {
			return nil, err
		}
		res, err := c.Call(ctx, method, prm)
		if err == nil {
			return res, nil
		}
		var re *RPCError
		if errors.As(err, &re) || NeedsAuth(err) || ctx.Err() != nil || retried || !p.again(method, err) {
			return nil, err
		}
		p.drop(c) // an expired session, a dropped pipe: once more on a new one
	}
}

// again says a request that failed on its way may be sent once more on a new
// connection. A tool call may have run before the answer was lost, so it is
// sent again only when the server turned it away unread (an http 4xx: an
// expired session, say); anything else is safe to repeat.
func (p *proxy) again(method string, err error) bool {
	if method != "tools/call" {
		return true
	}
	return strings.HasPrefix(err.Error(), "http 4")
}

func rpcErrorFor(server string, err error) *rpcError {
	var re *RPCError
	if errors.As(err, &re) {
		return &rpcError{Code: re.Code, Message: re.Message}
	}
	msg := server + ": " + err.Error()
	if NeedsAuth(err) {
		msg += " — sign in again: agent-tui mcp login " + server
	}
	return &rpcError{Code: -32603, Message: msg}
}
