package ui

import (
	"strings"
	"testing"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/session"
)

// Everything below is about the one risk caching introduces: a screen that
// shows what was true a moment ago. A frame that is fast and wrong is worse
// than a frame that is slow, so each of these changes one thing and insists
// the screen changed with it.

// shown is the whole frame as plain text.
func shown(m *Model) string { return stripANSI(m.View().Content) }

// The session list is remembered between frames. Everything a row is drawn
// from has to be in the key, or a conversation goes on saying what it was
// doing rather than what it is.
func TestTheSessionListNoticesEachThingItDrawsFrom(t *testing.T) {
	m := newTestModel(t)
	m.resize(140, 34)
	other := talking(m, "cuộc thứ hai")
	m.mgr.Select(0)
	m.View()

	for _, c := range []struct {
		what   string
		change func()
		expect string
	}{
		// Short, because a sidebar wraps a long one and the test would be
		// asking about the wrap rather than about the cache.
		{"a new title", func() { other.Title = "tênmới" }, "tênmới"},
		{"a turn starting", func() { other.Busy, other.Status = true, "đang chạy test" }, "đang chạy test"},
		{"a turn failing", func() {
			other.Busy, other.Status = false, ""
			other.LastErr = "rate limited"
		}, "failed"},
		{"an answer arriving unseen", func() { other.LastErr, other.Unseen = "", true }, "done"},
		{"a question for the reader", func() {
			m.approvals = append(m.approvals, pendingApproval{sess: other, ev: agent.EvApproval{}})
		}, "blocked"},
	} {
		c.change()
		if got := shown(m); !strings.Contains(got, c.expect) {
			t.Errorf("%s: the list still does not say %q", c.what, c.expect)
		}
	}
}

// A pane is remembered by everything it was drawn from, so new text in it has
// to reach the screen.
func TestAPaneRedrawsWhenItsContentChanges(t *testing.T) {
	m := newTestModel(t)
	m.resize(140, 34)
	s := m.mgr.Active()
	s.Append(session.Message{Role: session.RoleUser, Text: "câu hỏi đầu tiên"})
	m.invalidateChat()
	if got := shown(m); !strings.Contains(got, "câu hỏi đầu tiên") {
		t.Fatalf("the first message never appeared:\n%s", got)
	}

	s.Append(session.Message{Role: session.RoleAssistant, Text: "một câu trả lời mới"})
	m.invalidateChat()
	if got := shown(m); !strings.Contains(got, "một câu trả lời mới") {
		t.Errorf("the transcript is still showing the frame before it:\n%s", got)
	}
}

// Scrolling moves what is visible, and the viewport's output is remembered
// against where it is scrolled to.
func TestScrollingChangesWhatIsShown(t *testing.T) {
	m := newTestModel(t)
	m.resize(140, 34)
	s := m.mgr.Active()
	for i := 0; i < 60; i++ {
		s.Append(session.Message{Role: session.RoleUser, Text: "dòng đánh dấu " + string(rune('A'+i%26))})
	}
	m.invalidateChat()
	m.View()

	m.chat.GotoTop()
	top := m.chatView()
	m.chat.GotoBottom()
	bottom := m.chatView()

	if top == bottom {
		t.Error("scrolling from the top to the bottom showed the same lines")
	}
}

// And a streaming answer reaches the screen, which is the case the cache is
// most likely to get wrong: the committed half does not change while the tail
// changes on every delta.
func TestAStreamingAnswerReachesTheScreen(t *testing.T) {
	m := newTestModel(t)
	m.resize(140, 34)
	s := m.mgr.Active()
	s.Append(session.Message{Role: session.RoleUser, Text: "hỏi"})
	s.Busy = true
	m.invalidateChat()

	for _, tail := range []string{"Nó đang", "Nó đang nghĩ", "Nó đang nghĩ về resolvePfid"} {
		s.Partial = tail
		if got := shown(m); !strings.Contains(got, tail) {
			t.Errorf("the delta %q never reached the screen", tail)
		}
	}
}

// The prompt's frame is remembered too, and typing has to come through it.
func TestTypingReachesThePrompt(t *testing.T) {
	m := newTestModel(t)
	m.resize(140, 34)
	m.setFocus(focusInput)
	m.View()

	for _, r := range "resolvePfid" {
		m.onKey(key(string(r)))
	}
	if got := shown(m); !strings.Contains(got, "resolvePfid") {
		t.Errorf("what was typed is not on the screen:\n%s", got)
	}
}

// Resizing redraws everything, which is the case where a cache keyed on
// content alone would hand back a pane of the wrong width.
func TestResizingRedrawsAtTheNewWidth(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	s.Append(session.Message{Role: session.RoleUser, Text: "một câu"})
	m.invalidateChat()

	for _, w := range []int{100, 140, 90, 180} {
		m.resize(w, 34)
		m.invalidateChat()
		for i, l := range strings.Split(shown(m), "\n") {
			if n := len([]rune(l)); n > w {
				t.Fatalf("at %d columns, row %d is %d wide", w, i, n)
			}
		}
	}
}

// The pane cache is bounded. A conversation's text changes as it grows, and
// every version of it kept for ever is a leak with a nice name.
func TestThePaneCacheDoesNotGrowForEver(t *testing.T) {
	m := newTestModel(t)
	m.resize(140, 34)
	s := m.mgr.Active()
	for i := 0; i < 200; i++ {
		s.Append(session.Message{Role: session.RoleUser, Text: "câu " + string(rune('A'+i%26))})
		m.invalidateChat()
		m.View()
	}
	if n := len(m.paneOut); n > 64 {
		t.Errorf("the pane cache holds %d frames of panes", n)
	}
}

// Changing the theme has to reach the screen, and that is the hardest thing
// for a cache to get right: the frames are keyed on content and size, and a
// palette that changed underneath them would leave a screen half in each.
func TestChangingTheThemeRedrawsEverything(t *testing.T) {
	m := newTestModel(t)
	m.resize(140, 34)
	s := m.mgr.Active()
	s.Append(session.Message{Role: session.RoleUser, Text: "một câu"})
	m.invalidateChat()
	before := m.View().Content

	m.setTheme("monokai")

	after := m.View().Content
	if before == after {
		t.Error("the screen is identical after changing the theme")
	}
	// The same words, in different colours — not a different screen. The
	// status line is left out: it has just been told which theme this is, so
	// it is the one row that should read differently.
	body := func(v string) string {
		rows := strings.Split(stripANSI(v), "\n")
		return strings.Join(rows[:len(rows)-1], "\n")
	}
	if body(before) != body(after) {
		t.Errorf("changing the colours changed the text:\n%s\n---\n%s",
			body(before), body(after))
	}
	if !strings.Contains(m.notice, "monokai") {
		t.Errorf("nothing said which theme it is now: %q", m.notice)
	}
}

// And a theme that cannot be read is refused with the reason, rather than
// being applied and blamed on the program.
func TestAnUnreadableThemeIsRefusedFromTheCommand(t *testing.T) {
	m := newTestModel(t)
	was := m.st.P.Bg

	m.setTheme("no-such-theme-exists")

	if m.st.P.Bg != was {
		t.Error("a theme that does not exist was applied")
	}
	if !strings.Contains(m.notice, "theme:") {
		t.Errorf("it failed without saying so: %q", m.notice)
	}
}
