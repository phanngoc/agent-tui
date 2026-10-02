package ui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/phanngoc/agent-tui/internal/clipboard"
	"github.com/phanngoc/agent-tui/internal/session"
)

// Copying an answer for Slack.
//
// The conversion runs when, and only when, something is copied, and it runs
// inside the command rather than in Update: nothing about it is on the path
// that draws a frame or takes a streamed delta, so a transcript that is never
// copied from costs exactly what it did before.

// slackCopiedMsg reports a copy for Slack. err means the system clipboard was
// out of reach, and the text is sent through the terminal instead — it loses
// the HTML, but it still pastes as readable mrkdwn.
type slackCopiedMsg struct {
	what string
	text string
	err  error
}

// copyForSlack converts src and puts both forms on the clipboard.
func (m *Model) copyForSlack(src, what string) tea.Cmd {
	if strings.TrimSpace(src) == "" {
		m.notice = "nothing to copy yet"
		return nil
	}
	m.notice = "copying " + what + " for Slack…"
	return func() tea.Msg {
		htmlOut, text := slackExport(src)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return slackCopiedMsg{what: what, text: text, err: clipboard.WriteRich(ctx, htmlOut, text)}
	}
}

func (m *Model) slackCopied(msg slackCopiedMsg) tea.Cmd {
	if msg.err != nil {
		m.notice = "copy: " + msg.err.Error() + " — sent as text through the terminal"
		return tea.SetClipboard(msg.text)
	}
	m.notice = "copied " + msg.what + " for Slack — paste it there"
	return nil
}

// lastReply is the newest answer with something to say. An answer that was
// only tool calls has no text worth passing on.
func lastReply(s *session.Session) string {
	for i := len(s.Messages) - 1; i >= 0; i-- {
		if msg := s.Messages[i]; msg.Role == session.RoleAssistant && strings.TrimSpace(msg.Text) != "" {
			return msg.Text
		}
	}
	return ""
}

// replyInView is the answer the reader is looking at: of the answers on
// screen, the one that fills most of the pane, the newer one on a tie.
//
// Not simply the one the top line is in. Scrolled to the bottom, the top of
// the pane is often the tail of the previous answer while the screen is
// filled by the one below it, and that is the one being read.
func (m *Model) replyInView() string {
	s := m.mgr.Active()
	starts := m.chatStarts
	if len(starts) != len(s.Messages) || len(starts) == 0 {
		return lastReply(s)
	}
	top, bottom := m.chat.YOffset(), m.chat.YOffset()+m.chat.Height()
	total := m.chat.TotalLineCount()

	best, most := -1, 0
	for i, from := range starts {
		msg := s.Messages[i]
		if msg.Role != session.RoleAssistant || strings.TrimSpace(msg.Text) == "" {
			continue
		}
		to := total
		if i+1 < len(starts) {
			to = starts[i+1]
		}
		if seen := min(to, bottom) - max(from, top); seen > 0 && seen >= most {
			best, most = i, seen
		}
	}
	if best < 0 {
		return lastReply(s)
	}
	return s.Messages[best].Text
}
