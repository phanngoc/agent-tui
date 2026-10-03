package ui

import (
	"github.com/phanngoc/agent-tui/internal/session"
)

// One prompt box, a draft per conversation.
//
// The box is shared — a second text area would double history, completion,
// paste and the mouse for no gain — but what is typed in it belongs to the
// conversation it is addressed to. Half a question to one session used to
// follow you into the next, where enter sent it to the wrong agent; and the
// side chat had no way to be typed to at all once the caret left its pane,
// because the box said whatever had the focus was the one it addressed.
//
// So the box remembers who it is talking to (askSide), and the text it holds
// is stashed under that conversation's ID whenever that changes and brought
// back when it is addressed again. The side chat is a session with an ID of
// its own, so it gets a draft of its own by the same rule.

// promptDraft is a stashed prompt: the text, and the images its [image #n]
// markers refer to, which are numbered within it and only mean anything
// alongside it.
type promptDraft struct {
	text   string
	attach []session.Attachment
}

// syncPrompt swaps the box's contents when the conversation it addresses has
// changed since it was last looked at. It is cheap when nothing changed — one
// comparison — so it runs after every update rather than being remembered at
// each of the places that can move it.
func (m *Model) syncPrompt() {
	s := m.promptTarget()
	if s == nil || s.ID == m.promptOf {
		return
	}
	// The first look adopts whatever the box already holds: nothing was
	// addressed before, so there is nobody else it could belong to.
	if m.promptOf == "" {
		m.promptOf = s.ID
		return
	}
	if text := m.input.Value(); text != "" || len(m.attach) > 0 {
		if m.drafts == nil {
			m.drafts = map[string]promptDraft{}
		}
		m.drafts[m.promptOf] = promptDraft{text: text, attach: m.attach}
	} else {
		delete(m.drafts, m.promptOf)
	}
	d := m.drafts[s.ID]
	delete(m.drafts, s.ID)
	m.input.ClearSelection()
	m.input.SetValue(d.text)
	m.input.CursorEnd()
	m.attach = d.attach
	m.inputDrag = false
	m.closeCompletion()
	m.histIdx, m.histDraft = len(m.history), ""
	m.promptOf = s.ID
	m.input.Placeholder = promptHint
	if s.SideOf != "" {
		m.input.Placeholder = sideHint
	}
}

const (
	promptHint = "Ask anything, or press ctrl+p to open a file"
	sideHint   = "Ask beside this conversation · click the transcript to talk to it again"
)

// drafted reports whether a conversation the box is not addressing has
// something typed and waiting for it. The one it is addressing needs no
// telling: its draft is in front of you.
func (m *Model) drafted(s *session.Session) bool {
	_, ok := m.drafts[s.ID]
	return ok
}
