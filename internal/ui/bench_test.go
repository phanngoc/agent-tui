package ui

import (
	"strconv"
	"testing"

	"github.com/phanngoc/agent-tui/internal/session"
)

// A frame is drawn on every spinner tick, every keystroke and every movement
// of the mouse, so what it costs is what the program costs to sit in front of.
// These are here so that a change which makes it slower has to say so.
func loaded(b *testing.B, msgs, sessions int) *Model {
	b.Helper()
	m := newTestModel(&testing.T{})
	m.resize(180, 50)
	s := m.mgr.Active()
	for i := 0; i < msgs; i++ {
		role := session.RoleUser
		if i%2 == 1 {
			role = session.RoleAssistant
		}
		s.Append(session.Message{Role: role,
			Text: "câu số " + strconv.Itoa(i) + " nói về resolvePfid và onboarding.service.ts"})
	}
	for i := 1; i < sessions; i++ {
		o := m.mgr.New()
		o.Title = "hội thoại " + strconv.Itoa(i)
		o.Append(session.Message{Role: session.RoleUser, Text: "hỏi"})
	}
	m.mgr.Select(m.indexOf(s))
	m.invalidateChat()
	m.View()
	return m
}

// A frame with nothing moving, which is most of them.
func BenchmarkFrame(b *testing.B) {
	m := loaded(b, 200, 8)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.View()
	}
}

// And one with an answer arriving, where the committed half is unchanged and
// only the tail is not.
func BenchmarkFrameStreaming(b *testing.B) {
	m := loaded(b, 200, 8)
	s := m.mgr.Active()
	s.Busy = true
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Partial = "đang gõ dở một câu trả lời " + strconv.Itoa(i)
		m.View()
	}
}

func BenchmarkSessionLines(b *testing.B) {
	m := loaded(b, 200, 8)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.sessionLines(26)
	}
}
