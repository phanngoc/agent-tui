package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/phanngoc/agent-tui/internal/session"
)

// Local runs the commands for sessions no peer holds: the gateway's own
// runner.
type Local interface {
	Handle(cmd Command) error
}

// Peer is a process connected to the hub that runs turns of its own: a
// terminal app.
type Peer struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"` // tui
	Root     string    `json:"root"`
	PID      int       `json:"pid"`
	Joined   time.Time `json:"joined"`
	Seen     time.Time `json:"seen"`
	Sessions []string  `json:"sessions"`

	cmds  chan Command
	conns int
	held  map[string]bool
}

// Live is what is happening in a session right now, for a page that opens
// in the middle of a turn.
type Live struct {
	Session   string                      `json:"session"`
	Owner     string                      `json:"owner"`
	Busy      bool                        `json:"busy"`
	Status    string                      `json:"status,omitempty"`
	Prompt    string                      `json:"prompt,omitempty"`
	Engine    string                      `json:"engine,omitempty"`
	Started   time.Time                   `json:"started,omitempty"`
	Partial   string                      `json:"partial,omitempty"`
	Thinking  string                      `json:"thinking,omitempty"`
	Running   map[string]session.ToolCall `json:"running"`
	Output    map[string]string           `json:"output"`
	Approvals map[string]ApprovalData     `json:"approvals"`
	Choices   map[string]ChoiceData       `json:"choices"`
	Error     string                      `json:"error,omitempty"`
}

const ringSize = 4000

// Hub fans events out and routes commands.
type Hub struct {
	ID    string // the gateway's own origin name
	Local Local

	mu    sync.Mutex
	seq   int64
	ring  []Event
	subs  map[int]chan Event
	next  int
	peers map[string]*Peer
	live  map[string]*Live
}

// NewHub makes a hub; set Local before routing.
func NewHub() *Hub {
	return &Hub{ID: "gateway", subs: map[int]chan Event{}, peers: map[string]*Peer{}, live: map[string]*Live{}}
}

// Publish stamps an event and sends it to every subscriber.
func (h *Hub) Publish(e Event) Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	e.Seq = h.seq
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	if e.Origin == "" {
		e.Origin = h.ID
	}
	h.ring = append(h.ring, e)
	if len(h.ring) > ringSize {
		h.ring = append([]Event(nil), h.ring[len(h.ring)-ringSize/2:]...)
	}
	h.apply(e)
	for id, ch := range h.subs {
		select {
		case ch <- e:
		default:
			// A subscriber this far behind reconnects and replays from
			// its last seq; holding the hub for it would stall everyone.
			close(ch)
			delete(h.subs, id)
		}
	}
	return e
}

func (h *Hub) liveOf(id, owner string) *Live {
	l := h.live[id]
	if l == nil {
		l = &Live{Session: id}
		h.live[id] = l
	}
	if owner != "" {
		l.Owner = owner
	}
	if l.Running == nil {
		l.Running, l.Output = map[string]session.ToolCall{}, map[string]string{}
		l.Approvals, l.Choices = map[string]ApprovalData{}, map[string]ChoiceData{}
	}
	return l
}

// apply keeps Live current; mu is held.
func (h *Hub) apply(e Event) {
	if e.Session == "" {
		return
	}
	switch e.Type {
	case EvSessionDeleted:
		delete(h.live, e.Session)
		return
	case EvTurnStarted:
		var d TurnData
		_ = json.Unmarshal(e.Data, &d)
		l := h.liveOf(e.Session, e.Origin)
		*l = Live{Session: e.Session, Owner: e.Origin, Busy: true, Status: "thinking",
			Prompt: d.Prompt, Engine: d.Engine, Started: e.At}
		h.liveOf(e.Session, "")
		return
	}
	l, ok := h.live[e.Session]
	if !ok {
		return
	}
	switch e.Type {
	case EvTextDelta:
		var d TextData
		_ = json.Unmarshal(e.Data, &d)
		l.Partial += d.Text
	case EvThinkingDelta:
		var d TextData
		_ = json.Unmarshal(e.Data, &d)
		if len(l.Thinking) < 64<<10 {
			l.Thinking += d.Text
		}
	case EvStatus:
		var d TextData
		_ = json.Unmarshal(e.Data, &d)
		l.Status = d.Text
	case EvMessage:
		l.Partial, l.Thinking = "", ""
	case EvToolStart:
		var d ToolData
		_ = json.Unmarshal(e.Data, &d)
		l.Running[d.Call.ID] = d.Call
	case EvToolOutput:
		var d OutputData
		_ = json.Unmarshal(e.Data, &d)
		if out := l.Output[d.ID] + d.Text; len(out) > 32<<10 {
			l.Output[d.ID] = out[len(out)-32<<10:]
		} else {
			l.Output[d.ID] = out
		}
	case EvToolDone:
		var d ToolData
		_ = json.Unmarshal(e.Data, &d)
		delete(l.Running, d.Call.ID)
		delete(l.Output, d.Call.ID)
	case EvApprovalRequest:
		var d ApprovalData
		_ = json.Unmarshal(e.Data, &d)
		l.Approvals[d.ID] = d
	case EvApprovalDone:
		var d ResolvedData
		_ = json.Unmarshal(e.Data, &d)
		delete(l.Approvals, d.ID)
	case EvChoiceRequest:
		var d ChoiceData
		_ = json.Unmarshal(e.Data, &d)
		l.Choices[d.ID] = d
	case EvChoiceDone:
		var d ResolvedData
		_ = json.Unmarshal(e.Data, &d)
		delete(l.Choices, d.ID)
	case EvTurnDone:
		var d TurnData
		_ = json.Unmarshal(e.Data, &d)
		owner := l.Owner
		*l = Live{Session: e.Session, Owner: owner, Error: d.Error}
		h.liveOf(e.Session, "")
	}
}

// Subscribe returns the events after seq still in the ring, and a channel of
// what follows. cancel must be called.
func (h *Hub) Subscribe(after int64) ([]Event, <-chan Event, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var replay []Event
	if after > 0 {
		for _, e := range h.ring {
			if e.Seq > after {
				replay = append(replay, e)
			}
		}
	}
	ch := make(chan Event, 1024)
	id := h.next
	h.next++
	h.subs[id] = ch
	return replay, ch, func() {
		h.mu.Lock()
		if c, ok := h.subs[id]; ok {
			close(c)
			delete(h.subs, id)
		}
		h.mu.Unlock()
	}
}

// Seq is the newest event's number.
func (h *Hub) Seq() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.seq
}

// LiveAll snapshots every session's live state.
func (h *Hub) LiveAll() map[string]Live {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[string]Live, len(h.live))
	for k, v := range h.live {
		b, _ := json.Marshal(v)
		var cp Live
		_ = json.Unmarshal(b, &cp)
		out[k] = cp
	}
	return out
}

// Busy reports whether a session is mid-turn anywhere.
func (h *Hub) Busy(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := h.live[id]
	return l != nil && l.Busy
}

func randID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Join registers a peer.
func (h *Hub) Join(kind, root string, pid int) *Peer {
	p := &Peer{ID: kind + "-" + randID(), Kind: kind, Root: root, PID: pid,
		Joined: time.Now().UTC(), Seen: time.Now().UTC(), cmds: make(chan Command, 64), held: map[string]bool{}}
	h.mu.Lock()
	h.peers[p.ID] = p
	h.mu.Unlock()
	h.Publish(New(EvPeerJoined, "", p.public()))
	return p
}

func (p *Peer) public() Peer {
	cp := *p
	cp.Sessions = append([]string{}, p.Sessions...)
	return cp
}

// Peer finds a peer.
func (h *Hub) Peer(id string) (*Peer, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.peers[id]
	if ok {
		p.Seen = time.Now().UTC()
	}
	return p, ok
}

// Peers lists the connected peers.
func (h *Hub) Peers() []Peer {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Peer, 0, len(h.peers))
	for _, p := range h.peers {
		out = append(out, p.public())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Joined.Before(out[j].Joined) })
	return out
}

// Hold records which sessions a peer has open.
func (h *Hub) Hold(peerID string, ids []string) {
	h.mu.Lock()
	p, ok := h.peers[peerID]
	if ok {
		p.Sessions = append([]string{}, ids...)
		p.held = map[string]bool{}
		for _, id := range ids {
			p.held[id] = true
		}
		p.Seen = time.Now().UTC()
	}
	h.mu.Unlock()
}

// Attach counts a command stream opening; Detach its closing. A peer whose
// last stream has been closed for a grace period has left.
func (h *Hub) Attach(p *Peer) chan Command {
	h.mu.Lock()
	p.conns++
	h.mu.Unlock()
	return p.cmds
}

func (h *Hub) Detach(p *Peer) {
	h.mu.Lock()
	p.conns--
	h.mu.Unlock()
	time.AfterFunc(10*time.Second, func() {
		h.mu.Lock()
		if p.conns > 0 {
			h.mu.Unlock()
			return
		}
		delete(h.peers, p.ID)
		var orphaned []string
		for id, l := range h.live {
			if l.Owner == p.ID && l.Busy {
				orphaned = append(orphaned, id)
			}
		}
		h.mu.Unlock()
		h.Publish(New(EvPeerLeft, "", p.public()))
		for _, id := range orphaned {
			h.Publish(Event{Type: EvTurnDone, Session: id, Origin: p.ID,
				Data: mustJSON(TurnData{Error: "the terminal running this turn went away"})})
		}
	})
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// Owner names who runs a session: a peer holding it, or the gateway.
func (h *Hub) Owner(id string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, p := range h.peers {
		if p.held[id] {
			return p.ID
		}
	}
	return h.ID
}

// ErrBusy is returned for a command a peer's queue cannot take.
var ErrBusy = errors.New("the terminal holding this session is not taking commands")

// Route sends a command to whoever runs the session.
func (h *Hub) Route(cmd Command) (string, error) {
	owner := h.Owner(cmd.Session)
	if owner == h.ID {
		if h.Local == nil {
			return owner, errors.New("this gateway cannot run sessions")
		}
		return owner, h.Local.Handle(cmd)
	}
	h.mu.Lock()
	p := h.peers[owner]
	h.mu.Unlock()
	if p == nil {
		return owner, errors.New("peer gone")
	}
	select {
	case p.cmds <- cmd:
		return owner, nil
	default:
		return owner, ErrBusy
	}
}
