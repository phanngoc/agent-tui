package ui

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/gateway"
	"github.com/phanngoc/agent-tui/internal/learn"
	"github.com/phanngoc/agent-tui/internal/session"
)

// The terminal app is a peer of the gateway: what happens in its sessions is
// published as it happens, and what the web asks of them — a prompt, a stop,
// an answer to an approval — arrives as a command and is handled exactly as
// if it had been typed here. The session stays this process's to write, so
// the two views can never disagree about what it contains.

// SetGateway connects the model to a gateway client. Nil leaves it offline.
func (m *Model) SetGateway(c *gateway.Client) { m.gw = c }

// UseKit gives this app's turns what the project adds: instructions, skills,
// memory and MCP servers.
func (m *Model) UseKit() { m.useKit = true }

// SetLearner turns on automatic learning from this app's turns. Without one
// (as in tests) a finished turn teaches nothing.
func (m *Model) SetLearner(l *learn.Learner) { m.learner = l }

type gwCmdMsg struct{ cmd gateway.Command }
type gwTickMsg struct{}

func (m *Model) listenGateway() tea.Cmd {
	if m.gw == nil {
		return nil
	}
	ch := m.gw.Commands()
	return func() tea.Msg { return gwCmdMsg{<-ch} }
}

func (m *Model) gatewayTick() tea.Cmd {
	if m.gw == nil {
		return nil
	}
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return gwTickMsg{} })
}

// onGatewayTick tells the gateway which sessions this process holds.
func (m *Model) onGatewayTick() tea.Cmd {
	ids := make([]string, 0, m.mgr.Len())
	for _, s := range m.mgr.All() {
		ids = append(ids, s.ID)
	}
	if m.gw.Connected() && !m.gwSaid {
		// Once, quietly: where the web view of these same sessions is.
		m.gwSaid = true
		if m.notice == "" {
			m.notice = "live in the web admin too: http://" + m.gw.Addr()
		}
	}
	key := strings.Join(ids, ",")
	if key != m.gwHeld {
		m.gwHeld = key
		m.gw.Hold(ids)
	}
	return m.gatewayTick()
}

func gwID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (m *Model) publish(e gateway.Event) {
	if m.gw != nil {
		m.gw.Publish(e)
	}
}

func (m *Model) publishSummary(s *session.Session) {
	if m.gw == nil {
		return
	}
	sum := gateway.SummaryOf(s)
	e := gateway.New(gateway.EvSessionUpdated, s.ID, sum)
	e.Root = s.Root
	m.publish(e)
}

// publishTurnStart announces a turn and the prompt that began it.
func (m *Model) publishTurnStart(s *session.Session, engID, prompt string) {
	if m.gw == nil {
		return
	}
	e := gateway.New(gateway.EvTurnStarted, s.ID, gateway.TurnData{Prompt: prompt, Engine: engID})
	e.Root = s.Root
	m.publish(e)
	if n := len(s.Messages); n > 0 {
		e := gateway.New(gateway.EvMessage, s.ID, gateway.MessageData{Index: n - 1, Message: s.Messages[n-1]})
		e.Root = s.Root
		m.publish(e)
	}
	m.publishSummary(s)
}

// publishAgent mirrors one engine event. It is called before the event is
// applied, so an assistant message's index is the length it will extend.
func (m *Model) publishAgent(s *session.Session, ev agent.Event) {
	if m.gw == nil {
		return
	}
	switch e := ev.(type) {
	case agent.EvAssistant:
		out := gateway.New(gateway.EvMessage, s.ID, gateway.MessageData{Index: len(s.Messages), Message: e.Message})
		out.Root = s.Root
		m.publish(out)
		return
	case agent.EvApproval, agent.EvChoice:
		return // published by the queue, which gives them their ids
	}
	if out := gateway.FromAgent(s.ID, ev); out != nil {
		out.Root = s.Root
		m.publish(*out)
	}
}

// afterTurn is everything a finished turn owes the rest of the app: the
// gateway's list, and the learner.
func (m *Model) afterTurn(s *session.Session) {
	m.publishSummary(s)
	if m.learner != nil {
		m.learner.Notify(s.Root, s.ID, s.Messages)
	}
}

func (m *Model) publishApproval(s *session.Session, p pendingApproval) {
	e := gateway.New(gateway.EvApprovalRequest, s.ID, gateway.ApprovalData{ID: p.id, Call: p.ev.Call, Reason: p.ev.Reason})
	e.Root = s.Root
	m.publish(e)
}

func (m *Model) publishChoice(s *session.Session, p pendingChoice) {
	e := gateway.New(gateway.EvChoiceRequest, s.ID, gateway.ChoiceData{ID: p.id, Call: p.ev.Call,
		Question: p.ev.Question, Options: p.ev.Options})
	e.Root = s.Root
	m.publish(e)
}

func (m *Model) publishResolved(s *session.Session, typ, id, verdict string, index int, by string) {
	e := gateway.New(typ, s.ID, gateway.ResolvedData{ID: id, Verdict: verdict, Index: index, By: by})
	e.Root = s.Root
	m.publish(e)
}

// onGatewayCommand handles what the web asked of a session held here.
func (m *Model) onGatewayCommand(c gateway.Command) tea.Cmd {
	next := m.listenGateway()
	s := m.mgr.Get(c.Session)
	if s == nil {
		return next
	}
	switch c.Type {
	case gateway.CmdPrompt:
		text := strings.TrimSpace(c.Text)
		if text == "" {
			return next
		}
		if s.Busy {
			s.Queued = append(s.Queued, session.Queued{Text: text})
			m.notice = "a prompt from the web is queued behind this turn"
			return next
		}
		m.notice = "prompt from the web: " + firstLineOf(text)
		return tea.Batch(next, m.startTurn(s, text, nil))
	case gateway.CmdCancel:
		m.cancelSession(s)
		m.notice = "stopped from the web"
	case gateway.CmdApprove:
		for i, p := range m.approvals {
			if p.id != c.ID || p.sess != s {
				continue
			}
			m.approvals = append(m.approvals[:i:i], m.approvals[i+1:]...)
			v := gateway.VerdictOf(c.Verdict)
			replyOnce(p.ev.Reply, v)
			m.publishResolved(s, gateway.EvApprovalDone, p.id, gateway.VerdictName(v), 0, "web")
			if i == 0 && m.overlay == overlayApproval {
				m.showNextApproval()
			}
			break
		}
	case gateway.CmdChoose:
		for i, p := range m.choices {
			if p.id != c.ID || p.sess != s {
				continue
			}
			m.choices = append(m.choices[:i:i], m.choices[i+1:]...)
			replyChoice(p.ev.Reply, c.Index)
			m.publishResolved(s, gateway.EvChoiceDone, p.id, "", c.Index, "web")
			if i == 0 && m.overlay == overlayChoice {
				m.showNextChoice()
			}
			break
		}
	}
	m.invalidateChat()
	return next
}

// cancelSession stops one session's turn, wherever it is in the list.
func (m *Model) cancelSession(s *session.Session) {
	if cancel, ok := m.runs[s.ID]; ok {
		cancel()
		delete(m.runs, s.ID)
	}
	m.dropApprovals(s)
	m.dropChoices(s)
	s.Busy = false
	s.Status = ""
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 60 {
		s = s[:60] + "…"
	}
	return s
}
