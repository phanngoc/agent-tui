// Package browser lets the agent drive the user's own Chrome, through an
// extension, the way Claude in Chrome does: the extension asks the gateway
// for work, does it in the tabs it was given, and answers.
//
// The extension reaches the gateway; the gateway never reaches the browser.
// It polls for a command (a long poll, so a command arrives at once), runs
// it with the chrome.* APIs, and posts the result. Nothing is listening in
// the browser, and nothing needs a websocket library here.
//
// An extension is trusted only once the user approves it in the admin: it
// asks with a code it also shows in its own popup, the user approves that
// code, and from then on it carries a token. A web page can call the
// gateway too, but it can never hold that token.
//
// What the agent may touch is decided in the browser: the tabs it opened
// itself, gathered in an "agent-tui" tab group, and the ones the user
// shared from the popup. Nothing else.
package browser

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/phanngoc/agent-tui/internal/config"
)

// Poll is how long a command poll waits before answering empty.
const Poll = 25 * time.Second

// online is how recently an extension must have polled to count as there.
const online = Poll + 20*time.Second

// Client is an extension the user approved.
type Client struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Version  string    `json:"version"`
	Token    string    `json:"token_sha256"` // the token's hash: the token itself is only ever with the extension
	Approved time.Time `json:"approved"`
	LastSeen time.Time `json:"last_seen"`
}

// Pending is an extension asking to be approved.
type Pending struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Version string    `json:"version"`
	Code    string    `json:"code"`
	At      time.Time `json:"at"`
}

// Command is one thing for the browser to do.
type Command struct {
	ID     string          `json:"id"`
	Action string          `json:"action"`
	Args   json.RawMessage `json:"args,omitempty"`
}

// Result is how it went.
type Result struct {
	ID    string          `json:"id"`
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

// Hub is the gateway's side: the approved extensions, the ones asking, and
// the commands in flight.
type Hub struct {
	file string

	mu      sync.Mutex
	clients []Client
	pending map[string]*Pending
	issued  map[string]string // client -> token, handed over once after approval
	polled  map[string]time.Time
	waiting map[string]chan Result
	cmds    chan Command
}

// New opens the hub, its approvals kept in file (browser.json in the data
// directory when file is empty).
func New(file string) *Hub {
	if file == "" {
		file = filepath.Join(config.DataDir(), "browser.json")
	}
	h := &Hub{file: file, pending: map[string]*Pending{}, issued: map[string]string{},
		polled: map[string]time.Time{}, waiting: map[string]chan Result{}, cmds: make(chan Command, 16)}
	if b, err := os.ReadFile(file); err == nil {
		var st struct {
			Clients []Client `json:"clients"`
		}
		if json.Unmarshal(b, &st) == nil {
			h.clients = st.Clients
		}
	}
	return h
}

func (h *Hub) save() {
	b, _ := json.MarshalIndent(map[string]any{"clients": h.clients}, "", "  ")
	_ = os.MkdirAll(filepath.Dir(h.file), 0o755)
	tmp := h.file + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, h.file)
	}
}

func hash(token string) string {
	s := sha256.Sum256([]byte(token))
	return hex.EncodeToString(s[:])
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (h *Hub) client(id string) *Client {
	for i := range h.clients {
		if h.clients[i].ID == id {
			return &h.clients[i]
		}
	}
	return nil
}

// Hello is an extension saying it is there. With a token it was given it is
// in; just approved, it is handed its token; otherwise it is asked to wait,
// with a code to match in the admin.
func (h *Hub) Hello(id, name, version, token string) (status, newToken, code string) {
	if id == "" {
		return "error", "", ""
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if c := h.client(id); c != nil {
		if token != "" && hash(token) == c.Token {
			c.LastSeen, c.Name, c.Version = time.Now().UTC(), name, version
			return "ok", "", ""
		}
		if t, ok := h.issued[id]; ok {
			delete(h.issued, id)
			c.LastSeen = time.Now().UTC()
			return "ok", t, ""
		}
	}
	p, ok := h.pending[id]
	if !ok {
		n, _ := rand.Int(rand.Reader, big.NewInt(9000))
		p = &Pending{ID: id, Code: fmt.Sprintf("%04d", n.Int64()+1000), At: time.Now().UTC()}
		h.pending[id] = p
	}
	p.Name, p.Version = name, version
	return "pending", "", p.Code
}

// Approve lets the extension that asked with this id (or code) in.
func (h *Hub) Approve(idOrCode string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	var p *Pending
	for _, x := range h.pending {
		if x.ID == idOrCode || x.Code == idOrCode {
			p = x
		}
	}
	if p == nil {
		return errors.New("no extension is asking with " + idOrCode)
	}
	token := randHex(24)
	now := time.Now().UTC()
	if c := h.client(p.ID); c != nil {
		c.Token, c.Approved = hash(token), now
	} else {
		h.clients = append(h.clients, Client{ID: p.ID, Name: p.Name, Version: p.Version, Token: hash(token), Approved: now})
	}
	h.issued[p.ID] = token
	delete(h.pending, p.ID)
	h.save()
	return nil
}

// Reject turns a request down.
func (h *Hub) Reject(idOrCode string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for k, x := range h.pending {
		if x.ID == idOrCode || x.Code == idOrCode {
			delete(h.pending, k)
		}
	}
}

// Revoke forgets an approved extension: its token stops working.
func (h *Hub) Revoke(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	kept := h.clients[:0]
	for _, c := range h.clients {
		if c.ID != id {
			kept = append(kept, c)
		}
	}
	h.clients = kept
	delete(h.issued, id)
	delete(h.polled, id)
	h.save()
}

// Auth reports whether the token is this extension's.
func (h *Hub) Auth(id, token string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	c := h.client(id)
	return c != nil && token != "" && hash(token) == c.Token
}

// Next waits for a command for an approved extension, up to Poll.
func (h *Hub) Next(ctx context.Context, id string) (*Command, bool) {
	h.mu.Lock()
	h.polled[id] = time.Now()
	if c := h.client(id); c != nil {
		c.LastSeen = time.Now().UTC()
	}
	h.mu.Unlock()
	t := time.NewTimer(Poll)
	defer t.Stop()
	select {
	case c := <-h.cmds:
		return &c, true
	case <-t.C:
	case <-ctx.Done():
	}
	h.mu.Lock()
	h.polled[id] = time.Now()
	h.mu.Unlock()
	return nil, false
}

// Deliver hands a result to the call waiting for it.
func (h *Hub) Deliver(r Result) {
	h.mu.Lock()
	ch := h.waiting[r.ID]
	delete(h.waiting, r.ID)
	h.mu.Unlock()
	if ch != nil {
		ch <- r
	}
}

// Connected reports whether an approved extension is polling.
func (h *Hub) Connected() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, at := range h.polled {
		if time.Since(at) < online && h.client(id) != nil {
			return true
		}
	}
	return false
}

// ErrOffline is a command with no extension to run it.
var ErrOffline = errors.New("Chrome is not connected: install the agent-tui extension and approve it on the admin's Browser page")

// Do runs a command in the browser and waits for its result.
func (h *Hub) Do(ctx context.Context, action string, args any) (Result, error) {
	if !h.Connected() {
		return Result{}, ErrOffline
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return Result{}, err
	}
	c := Command{ID: randHex(8), Action: action, Args: raw}
	ch := make(chan Result, 1)
	h.mu.Lock()
	h.waiting[c.ID] = ch
	h.mu.Unlock()
	forget := func() {
		h.mu.Lock()
		delete(h.waiting, c.ID)
		h.mu.Unlock()
	}
	select {
	case h.cmds <- c:
	case <-ctx.Done():
		forget()
		return Result{}, ctx.Err()
	}
	select {
	case r := <-ch:
		if !r.OK {
			return r, errors.New(r.Error)
		}
		return r, nil
	case <-ctx.Done():
		forget()
		return Result{}, fmt.Errorf("the browser did not answer %s: %w", action, ctx.Err())
	}
}

// Status is what the admin shows.
type Status struct {
	Connected bool      `json:"connected"`
	Clients   []Client  `json:"clients"`
	Pending   []Pending `json:"pending"`
}

// Status reads the hub.
func (h *Hub) Status() Status {
	connected := h.Connected()
	h.mu.Lock()
	defer h.mu.Unlock()
	st := Status{Connected: connected, Clients: []Client{}, Pending: []Pending{}}
	for _, c := range h.clients {
		c.Token = ""
		st.Clients = append(st.Clients, c)
	}
	for _, p := range h.pending {
		if time.Since(p.At) < time.Hour {
			st.Pending = append(st.Pending, *p)
		}
	}
	sort.Slice(st.Pending, func(i, j int) bool { return st.Pending[i].At.After(st.Pending[j].At) })
	return st
}
