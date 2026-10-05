package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Pool keeps one connection per server definition and reuses it across turns
// and sessions. A definition that changes is a different key, so editing a
// server in the admin reconnects it on next use; the stale connection is
// closed by Prune.
type Pool struct {
	mu      sync.Mutex
	clients map[string]*entry
}

type entry struct {
	client *Client
	err    error
	at     time.Time
	tools  []Tool
	ready  chan struct{}
}

// Status is what the admin shows for a server.
type Status struct {
	Name      string    `json:"name"`
	Connected bool      `json:"connected"`
	Error     string    `json:"error,omitempty"`
	Tools     []Tool    `json:"tools"`
	Server    string    `json:"server_info,omitempty"`
	At        time.Time `json:"at"`
	// NeedsAuth says the server refused for want of a sign-in, and SignedIn
	// that one is kept for it.
	NeedsAuth bool `json:"needs_auth,omitempty"`
	SignedIn  bool `json:"signed_in,omitempty"`
}

// ToolRef is a server's tool as the agent sees it.
type ToolRef struct {
	Server string
	Tool   Tool
}

// QualifiedName is the name the model calls: mcp__<server>__<tool>, the
// convention Claude Code uses, so the same server's tools read the same in
// both engines.
func (r ToolRef) QualifiedName() string {
	return clean("mcp__" + r.Server + "__" + r.Tool.Name)
}

// clean makes a name the API accepts: letters, digits, '_' and '-', at most
// 128 of them. Tool names with dots or slashes exist; they are called by
// their real name, and only shown to the model under this one.
func clean(s string) string {
	b := []byte(s)
	for i, c := range b {
		ok := c == '_' || c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if !ok {
			b[i] = '_'
		}
	}
	if len(b) > 128 {
		b = b[:128]
	}
	return string(b)
}

// SplitName undoes QualifiedName.
func SplitName(q string) (server, tool string, ok bool) {
	rest, found := strings.CutPrefix(q, "mcp__")
	if !found {
		return "", "", false
	}
	server, tool, ok = strings.Cut(rest, "__")
	return
}

func NewPool() *Pool { return &Pool{clients: map[string]*entry{}} }

func key(s Server) string {
	s.Scope, s.Shadowed = "", false
	b, _ := json.Marshal(s)
	return string(b)
}

// get connects on first use. A failure is remembered for a minute so a dead
// server does not cost every turn a connection timeout.
func (p *Pool) get(ctx context.Context, s Server) *entry {
	k := key(s)
	p.mu.Lock()
	e, ok := p.clients[k]
	if ok && e.err != nil && time.Since(e.at) > time.Minute {
		ok = false
	}
	if !ok {
		e = &entry{ready: make(chan struct{})}
		p.clients[k] = e
		p.mu.Unlock()
		go func() {
			defer close(e.ready)
			cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			c, err := Connect(cctx, s)
			if err == nil {
				e.tools, err = c.Tools(cctx)
				if err != nil {
					c.Close()
					c = nil
				}
			}
			e.client, e.err, e.at = c, err, time.Now()
		}()
	} else {
		p.mu.Unlock()
	}
	select {
	case <-e.ready:
	case <-ctx.Done():
		return &entry{err: ctx.Err(), at: time.Now()}
	}
	return e
}

// Tools connects to every server (concurrently) and lists their tools. A
// server that fails is left out, and its error is in Statuses.
func (p *Pool) Tools(ctx context.Context, servers []Server) []ToolRef {
	var (
		mu  sync.Mutex
		out []ToolRef
		wg  sync.WaitGroup
	)
	for _, s := range servers {
		wg.Add(1)
		go func(s Server) {
			defer wg.Done()
			e := p.get(ctx, s)
			if e.err != nil {
				return
			}
			mu.Lock()
			for _, t := range e.tools {
				out = append(out, ToolRef{Server: s.Name, Tool: t})
			}
			mu.Unlock()
		}(s)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].QualifiedName() < out[j].QualifiedName() })
	return out
}

// Statuses connects to each server and reports how it went.
func (p *Pool) Statuses(ctx context.Context, servers []Server) []Status {
	out := make([]Status, len(servers))
	var wg sync.WaitGroup
	for i, s := range servers {
		wg.Add(1)
		go func(i int, s Server) {
			defer wg.Done()
			e := p.get(ctx, s)
			st := Status{Name: s.Name, At: e.at, Tools: e.tools}
			st.SignedIn = s.Transport() != "stdio" && SignedIn(s.URL)
			if e.err != nil {
				st.Error = e.err.Error()
				st.NeedsAuth = NeedsAuth(e.err)
			} else {
				st.Connected = true
				if e.client != nil {
					st.Server = strings.TrimSpace(e.client.Info.Name + " " + e.client.Info.Version)
				}
			}
			if st.Tools == nil {
				st.Tools = []Tool{}
			}
			out[i] = st
		}(i, s)
	}
	wg.Wait()
	return out
}

// Call runs a tool on the named server, which must be among servers.
func (p *Pool) Call(ctx context.Context, servers []Server, qualified string, args json.RawMessage) (string, bool) {
	name, tool, ok := SplitName(qualified)
	if !ok {
		return "not an MCP tool: " + qualified, true
	}
	return p.CallOn(ctx, servers, name, tool, args)
}

// CallOn runs a tool given its server and its own name.
func (p *Pool) CallOn(ctx context.Context, servers []Server, name, tool string, args json.RawMessage) (string, bool) {
	qualified := "mcp__" + name + "__" + tool
	for _, s := range servers {
		if s.Name != name {
			continue
		}
		e := p.get(ctx, s)
		if e.err != nil {
			return fmt.Sprintf("MCP server %s is unavailable: %v", name, e.err), true
		}
		out, isErr, err := e.client.CallTool(ctx, tool, args)
		if err != nil {
			return fmt.Sprintf("%s: %v", qualified, err), true
		}
		return out, isErr
	}
	return "no MCP server named " + name, true
}

// Forget drops a server's connection so the next use reconnects.
func (p *Pool) Forget(s Server) {
	p.mu.Lock()
	e, ok := p.clients[key(s)]
	delete(p.clients, key(s))
	p.mu.Unlock()
	if ok {
		go func() {
			<-e.ready
			if e.client != nil {
				e.client.Close()
			}
		}()
	}
}

// Close stops every server.
func (p *Pool) Close() {
	p.mu.Lock()
	all := p.clients
	p.clients = map[string]*entry{}
	p.mu.Unlock()
	for _, e := range all {
		select {
		case <-e.ready:
			if e.client != nil {
				e.client.Close()
			}
		default:
		}
	}
}
