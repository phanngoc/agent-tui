package gateway

import (
	"strings"
	"time"

	"github.com/phanngoc/agent-tui/internal/session"
)

// Messages sent while a turn runs, as Claude Code takes them
// (code.claude.com/docs/en/interactive-mode#queue-messages-while-claude-works):
//
//   - A message sent mid-turn is queued, and shown queued.
//   - If the agent is running tools, it gets the queued messages as soon as
//     those finish, within the same turn — steering — when its engine can
//     take them (agent.Steerer). They enter the transcript there, marked.
//   - What is still queued when the turn ends goes next, on its own, in the
//     order it was typed — after a stop as well: stopping is how to have it
//     heard now.
//   - Send now (Ctrl+Enter) stops the turn so the queue goes at once.
//   - A queued message can be taken back, to edit or drop.

// EvQueue is a session's queue, whenever it changes.
const EvQueue = "queue"

// Queued is a message waiting for the agent.
type Queued struct {
	ID   string    `json:"id"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
	// Files go with a turn of their own: a message with images waits for
	// the turn to end rather than steering it.
	Files []session.Attachment `json:"files,omitempty"`
}

// QueueData is EvQueue's data.
type QueueData struct {
	Items []Queued `json:"items"`
	// Steers says the running turn's engine takes them mid-turn; otherwise
	// they wait for the turn to end.
	Steers bool `json:"steers"`
}

// Queue is what is waiting for a session's agent.
func (r *Runner) Queue(id string) QueueData {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.queueData(id)
}

func (r *Runner) queueData(id string) QueueData {
	d := QueueData{Items: append([]Queued{}, r.queues[id]...)}
	if t := r.turns[id]; t != nil {
		d.Steers = t.steers
	}
	return d
}

func (r *Runner) publishQueue(id, root string) {
	r.mu.Lock()
	d := r.queueData(id)
	r.mu.Unlock()
	r.Hub.Publish(Event{Type: EvQueue, Session: id, Root: root, Data: mustJSON(d)})
}

// enqueue holds text for a session's running turn; now stops the turn, so
// it goes at once. It says false when no turn is running.
func (r *Runner) enqueue(id, text string, now bool, files ...session.Attachment) bool {
	r.mu.Lock()
	t := r.turns[id]
	if t == nil {
		r.mu.Unlock()
		return false
	}
	if strings.TrimSpace(text) != "" || len(files) > 0 {
		r.queues[id] = append(r.queues[id], Queued{ID: randID(), Text: text, At: time.Now(), Files: files})
	}
	root := t.s.Root
	if now {
		t.cancel()
	}
	r.mu.Unlock()
	r.publishQueue(id, root)
	return true
}

// Unqueue takes a queued message back, returning its text.
func (r *Runner) Unqueue(id, item string) (string, bool) {
	r.mu.Lock()
	q := r.queues[id]
	text, root, found := "", "", false
	for i, m := range q {
		if m.ID == item {
			text, found = m.Text, true
			r.queues[id] = append(q[:i:i], q[i+1:]...)
			break
		}
	}
	if t := r.turns[id]; t != nil {
		root = t.s.Root
	}
	r.mu.Unlock()
	if found {
		r.publishQueue(id, root)
	}
	return text, found
}

// takeQueue takes the queued messages the agent can be handed mid-turn:
// the text-only ones, up to the first with images, which waits for a turn of
// its own along with everything after it.
func (r *Runner) takeQueue(id string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	q := r.queues[id]
	n := 0
	for n < len(q) && len(q[n].Files) == 0 {
		n++
	}
	texts := make([]string, n)
	for i := range n {
		texts[i] = q[i].Text
	}
	if n == len(q) {
		delete(r.queues, id)
	} else {
		r.queues[id] = append([]Queued(nil), q[n:]...)
	}
	return texts
}

// takeAll empties a session's queue for the next turn: the texts, and the
// images that came with them.
func (r *Runner) takeAll(id string) ([]string, []session.Attachment) {
	r.mu.Lock()
	q := r.queues[id]
	delete(r.queues, id)
	r.mu.Unlock()
	var texts []string
	var files []session.Attachment
	for _, m := range q {
		if strings.TrimSpace(m.Text) != "" {
			texts = append(texts, m.Text)
		}
		files = append(files, m.Files...)
	}
	return texts, files
}

// steered records messages the agent was handed mid-turn, where it got
// them in the conversation.
func (r *Runner) steered(p *project, s *session.Session, texts []string) {
	for _, t := range texts {
		m := session.Message{Role: session.RoleUser, Text: t, At: time.Now(), Steered: true}
		s.Append(m)
		r.Hub.Publish(Event{Type: EvMessage, Session: s.ID, Root: s.Root,
			Data: mustJSON(MessageData{Index: len(s.Messages) - 1, Message: m})})
	}
	p.mgr.Save(s)
	r.publishQueue(s.ID, s.Root)
}

// next starts what is still queued when a turn ends, as one message.
func (r *Runner) next(p *project, s *session.Session) {
	r.mu.Lock()
	closing := r.closing
	r.mu.Unlock()
	if closing {
		return
	}
	texts, files := r.takeAll(s.ID)
	if len(texts) == 0 && len(files) == 0 {
		return
	}
	r.publishQueue(s.ID, s.Root)
	_ = r.start(p, s, strings.Join(texts, "\n\n"), false, files...)
}
