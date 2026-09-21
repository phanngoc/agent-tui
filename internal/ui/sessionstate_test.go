package ui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/session"
)

// talking gives a session something in it, so it is not simply new.
func talking(m *Model, title string) *session.Session {
	s := m.mgr.New()
	s.Title = title
	s.Append(session.Message{Role: session.RoleUser, Text: "hỏi"})
	s.Append(session.Message{Role: session.RoleAssistant, Text: "đáp"})
	return s
}

// Every state gets its own shape. Colour alone is not a signal: blocked,
// working and done were all a filled dot in the sidebar herdr had to change,
// and to a colourblind reader those three collapse into one.
func TestEveryStateHasItsOwnShape(t *testing.T) {
	seen := map[string]sessionState{}
	for _, st := range []sessionState{
		stateEmpty, stateIdle, stateUnseen, stateWorking, stateBlocked, stateFailed,
	} {
		if st == stateWorking {
			continue // a spinner, which is a shape that moves
		}
		g := st.glyph()
		if g == "" {
			t.Errorf("%s has no shape", st.word())
		}
		if was, dup := seen[g]; dup {
			t.Errorf("%s and %s are both %q", was.word(), st.word(), g)
		}
		seen[g] = st
	}
	// And each is one cell, or the column stops being a column.
	for g := range seen {
		if n := len([]rune(g)); n != 1 {
			t.Errorf("%q is %d runes wide", g, n)
		}
	}
}

// The word is said as well as drawn. A glyph nobody has learned yet is a
// decoration.
func TestEveryStateSaysItsName(t *testing.T) {
	for _, st := range []sessionState{
		stateEmpty, stateIdle, stateUnseen, stateWorking, stateBlocked, stateFailed,
	} {
		if strings.TrimSpace(st.word()) == "" {
			t.Errorf("state %d has no word", st)
		}
	}
}

// Waiting on the reader outranks everything: a session can be blocked and busy
// at once — the turn is running, and it is running because it is waiting — and
// the half you can do something about is the half worth showing.
func TestBlockedOutranksWorking(t *testing.T) {
	m := newTestModel(t)
	s := talking(m, "scan")
	s.Busy = true
	m.approvals = append(m.approvals, pendingApproval{sess: s, ev: agent.EvApproval{}})

	if got := m.sessionState(s); got != stateBlocked {
		t.Errorf("a session waiting on an approval reads as %q", got.word())
	}
	// A question put to the user blocks it just the same.
	m.approvals = nil
	m.choices = append(m.choices, pendingChoice{sess: s, ev: agent.EvChoice{}})
	if got := m.sessionState(s); got != stateBlocked {
		t.Errorf("a session waiting on a choice reads as %q", got.word())
	}
	// And an approval queued against another session does not block this one.
	other := talking(m, "khác")
	if got := m.sessionState(other); got == stateBlocked {
		t.Error("another session's question blocked this one")
	}
}

// A `!` command running is the session working, even though it does not make
// the session busy.
func TestAShellCommandCountsAsWorking(t *testing.T) {
	m := newTestModel(t)
	s := talking(m, "build")
	s.Running = 1

	if got := m.sessionState(s); got != stateWorking {
		t.Errorf("a session with a command in flight reads as %q", got.word())
	}
}

// The state worth having a list for: the answer arrived somewhere you were not.
func TestATurnEndingElsewhereIsMarkedUnseen(t *testing.T) {
	m := newTestModel(t)
	here := m.mgr.Active()
	there := talking(m, "ở nơi khác")
	m.mgr.Select(m.indexOf(here))

	m.markUnseen(there)
	if got := m.sessionState(there); got != stateUnseen {
		t.Errorf("a turn that finished elsewhere reads as %q", got.word())
	}

	// A turn finishing on the conversation you are looking at is not news.
	m.markUnseen(here)
	if here.Unseen {
		t.Error("the conversation on screen was marked unseen")
	}

	// And looking is what clears it.
	m.mgr.Select(m.indexOf(there))
	m.onSessionSwitch()
	if there.Unseen {
		t.Error("switching to it did not count as looking at it")
	}
}

// A failed turn says so rather than reading as finished.
func TestAFailedTurnHasItsOwnState(t *testing.T) {
	m := newTestModel(t)
	s := talking(m, "migrate")
	s.LastErr = "rate limited"

	if got := m.sessionState(s); got != stateFailed {
		t.Errorf("a session whose last turn failed reads as %q", got.word())
	}
}

// An empty conversation is not idle — there is nothing for it to be idle
// about — and it is the one thing the list showed no difference for at all.
func TestAnEmptyConversationIsNotIdle(t *testing.T) {
	m := newTestModel(t)
	if got := m.sessionState(m.mgr.New()); got != stateEmpty {
		t.Errorf("a conversation with nothing in it reads as %q", got.word())
	}
}

// The glyph column says what a conversation is doing; the row's background
// says where you are standing. They used to share the column, and a session
// that was both active and running lost the mark that said where you were.
func TestRunningAndActiveAreSaidSeparately(t *testing.T) {
	m := newTestModel(t)
	s := talking(m, "đang chạy ngay đây")
	s.Busy = true
	m.mgr.Select(m.indexOf(s))
	m.setFocus(focusChat) // so the selection is not also on this row

	rows := m.sessionLines(34)
	var line string
	for _, r := range rows {
		if m.mgr.All()[r.idx] == s {
			line = r.text
			break
		}
	}
	if line == "" {
		t.Fatal("the active session is not in the list")
	}
	// Still marked as running…
	if got := stripANSI(line); !strings.HasPrefix(strings.TrimSpace(got), m.spin.View()) &&
		!strings.Contains(got, "đang chạy") {
		t.Fatalf("the row is not the one expected: %q", got)
	}
	// …and still marked as where you are.
	if !strings.Contains(line, "48;2;") {
		t.Errorf("the active row lost its background while it was running:\n%q", line)
	}
}

// The state leads the line under the title, in its own colour, because it is
// the reason to look at that line at all.
func TestTheMetaLineLeadsWithTheState(t *testing.T) {
	m := newTestModel(t)
	s := talking(m, "t")
	s.LastErr = "boom"

	got := stripANSI(m.sessionMeta(s, 40))
	if !strings.HasPrefix(got, "failed") {
		t.Errorf("the line under the title reads %q, want the state first", got)
	}
	// A working session says what it is doing rather than how long ago it
	// last moved, which is not the interesting number while it is moving.
	s.LastErr, s.Busy, s.Status = "", true, "running tests"
	if got := stripANSI(m.sessionMeta(s, 40)); !strings.HasPrefix(got, "running tests") {
		t.Errorf("a working session reads %q", got)
	}
	if strings.Contains(stripANSI(m.sessionMeta(s, 40)), "ago") {
		t.Error("a working session is still counting how long since it last moved")
	}
}

// Whatever it says, it says it inside the pane.
func TestTheMetaLineFitsTheSidebar(t *testing.T) {
	m := newTestModel(t)
	s := talking(m, "t")
	s.Busy, s.Status = true, strings.Repeat("một trạng thái rất dài ", 10)

	for _, w := range []int{12, 20, 34, 60} {
		if got := ansi.StringWidth(m.sessionMeta(s, w)); got > w {
			t.Errorf("width %d: the line is %d columns", w, got)
		}
	}
}

// The shape, the colour and the word are one statement, so the spinner has to
// be the same colour as the word it stands next to. Two signals that disagree
// about what colour "working" is are two signals.
func TestTheSpinnerWearsTheWorkingColour(t *testing.T) {
	m := newTestModel(t)
	want := m.st.Warn.Render("x")
	got := m.spin.View()

	code := func(s string) string {
		if i := strings.Index(s, "m"); i > 0 {
			return s[:i]
		}
		return s
	}
	if code(got) != code(want) {
		t.Errorf("the spinner is %q and the working label is %q", code(got), code(want))
	}
}

// The tab strip speaks the same vocabulary as the list. A session blocked in a
// background tab used to be invisible up there: the strip knew only "busy".
func TestTheTabStripShowsWhatWantsYou(t *testing.T) {
	m := newTestModel(t)
	s := talking(m, "scan")
	m.approvals = append(m.approvals, pendingApproval{sess: s, ev: agent.EvApproval{}})

	if got := stripANSI(m.tabMark(s, false)); got != stateBlocked.glyph() {
		t.Errorf("a blocked session shows %q in the strip", got)
	}
	s.LastErr = ""
	m.approvals = nil
	s.Unseen = true
	if got := stripANSI(m.tabMark(s, false)); got != stateUnseen.glyph() {
		t.Errorf("a finished-elsewhere session shows %q in the strip", got)
	}
}

// And says nothing when there is nothing to say. Every state marked would be a
// row of ticks across the top, which is wallpaper rather than a glance.
func TestTheTabStripIsQuietWhenNothingWantsYou(t *testing.T) {
	m := newTestModel(t)
	idle := talking(m, "đã xong và đã xem")
	empty := m.mgr.New()

	if got := m.tabMark(idle, false); got != "" {
		t.Errorf("an idle session marked the strip with %q", stripANSI(got))
	}
	if got := m.tabMark(empty, false); got != "" {
		t.Errorf("an empty session marked the strip with %q", stripANSI(got))
	}
}

// On the active tab the shape stays and the colour goes: the tab is already
// inverted, and a state colour on that background fights the highlight.
func TestTheActiveTabKeepsTheShapeAndDropsTheColour(t *testing.T) {
	m := newTestModel(t)
	s := talking(m, "failed here")
	s.LastErr = "boom"

	// It holds for the spinner too, which is the one that used to keep its
	// yellow on an inverted tab.
	s.Busy = true
	if got := m.tabMark(s, true); strings.Contains(got, "\x1b") {
		t.Errorf("the active tab's spinner carries its own colour: %q", got)
	}
	if got := m.tabMark(s, false); !strings.Contains(got, "\x1b") {
		t.Error("an inactive tab's spinner lost its colour")
	}
	s.Busy = false

	on, off := m.tabMark(s, true), m.tabMark(s, false)
	if stripANSI(on) != stripANSI(off) {
		t.Errorf("the shape changed with the tab: %q vs %q", stripANSI(on), stripANSI(off))
	}
	if strings.Contains(on, "\x1b") {
		t.Errorf("the active tab's mark carries its own colour: %q", on)
	}
	if !strings.Contains(off, "\x1b") {
		t.Error("an inactive tab's mark lost its colour, which is where colour is worth having")
	}
}

// The strip and the sidebar are two views of the same conversations, so they
// have to agree about what is in the list. A side chat used to appear in the
// strip and nowhere else: a busy aside put a spinner up there with no row
// underneath it to match, which reads as the list having lost track.
func TestTheStripAndTheListAgreeAboutSideChats(t *testing.T) {
	m := newTestModel(t)
	withHistory(m)
	m.openBtw("")
	side := m.sideSession()
	if side == nil {
		t.Fatal("no side chat to test with")
	}
	side.Busy = true
	side.Title = "cái aside"

	if got := stripANSI(m.header()); strings.Contains(got, "cái aside") {
		t.Errorf("the aside has a tab of its own:\n%s", got)
	}
	for _, l := range m.sessionLines(30) {
		if m.mgr.All()[l.idx] == side {
			t.Error("the aside has a row of its own")
		}
	}
	// It is not invisible, though: it says what it is doing where it lives.
	if got := m.btwTitle(); !strings.Contains(got, stateWorking.word()) &&
		!strings.Contains(got, orDefault(side.Status, "working")) {
		t.Errorf("the aside's pane does not say it is running: %q", got)
	}
}

// A command run with ! is a line in the conversation, so the pane follows it
// down. It used to rewrite the transcript and leave the reader wherever they
// already were, which for a session with any history is above the thing that
// just happened.
func TestRunningACommandFollowsTheTranscript(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	for i := 0; i < 40; i++ {
		s.Append(session.Message{Role: session.RoleUser, Text: "dòng cũ " + strconv.Itoa(i)})
	}
	m.invalidateChat()
	m.View()
	m.chat.GotoTop()
	if m.chat.YOffset() != 0 {
		t.Fatalf("the pane would not go to the top: %d", m.chat.YOffset())
	}

	s.Append(session.Message{Role: session.RoleUser, Text: "mới", Shell: &session.ShellRun{
		Command: "cd ..", Done: true,
	}})
	m.grew(s)
	m.View()

	if m.chat.YOffset() == 0 {
		t.Error("the transcript grew and the pane stayed at the top")
	}
	if !m.chat.AtBottom() {
		t.Errorf("the pane did not follow the new line down: offset %d", m.chat.YOffset())
	}
}

// But it does not drag a pane the reader is not looking at.
func TestGrowingAnotherConversationDoesNotScrollThisOne(t *testing.T) {
	m := newTestModel(t)
	here := m.mgr.Active()
	for i := 0; i < 40; i++ {
		here.Append(session.Message{Role: session.RoleUser, Text: "dòng " + strconv.Itoa(i)})
	}
	m.invalidateChat()
	m.View()
	m.chat.GotoTop()

	there := talking(m, "ở nơi khác")
	m.mgr.Select(m.indexOf(here))
	m.grew(there)

	if m.chat.YOffset() != 0 {
		t.Errorf("another conversation growing moved this pane to %d", m.chat.YOffset())
	}
}

// crowd fills the list with more conversations than the pane can draw.
func crowd(t *testing.T, m *Model, n int) []*session.Session {
	t.Helper()
	m.resize(120, 40)
	var out []*session.Session
	for i := 0; i < n; i++ {
		out = append(out, talking(m, "cuộc hội thoại số "+strconv.Itoa(i)))
	}
	return out
}

// The list is a list, so it scrolls. It used to cut at the height of the whole
// body and hand the rest to a pane half that tall, which clipped the rest
// without a word — so the conversation that was running could be below the
// fold with nothing to say it existed, which is exactly how it looked.
func TestTheRunningConversationStaysInTheList(t *testing.T) {
	m := newTestModel(t)
	all := crowd(t, m, 9)
	// New puts each conversation at the top of the list, so the one made
	// first is the row furthest down — the row the old code cut off.
	busy := all[0]
	busy.Busy, busy.Status = true, "thinking"
	m.mgr.Select(m.indexOf(busy))
	m.setFocus(focusChat)

	sessH, _ := m.leftSplit()
	rows := m.sessionLines(max(4, m.sideW-2))
	if len(rows) <= sessH-2 {
		t.Skipf("everything fits at this size (%d rows in %d)", len(rows), sessH-2)
	}

	pane := stripANSI(m.sessionsPane())
	if n := strings.Count(pane, "\n") + 1; n > sessH-2 {
		t.Errorf("the pane was handed %d lines for %d", n, sessH-2)
	}
	if !strings.Contains(pane, stripANSI(m.spin.View())) {
		t.Errorf("the running conversation is not in the pane:\n%s", pane)
	}
	if !strings.Contains(pane, "thinking") {
		t.Errorf("the running conversation's state is not in the pane:\n%s", pane)
	}
}

// And moving the cursor through the list brings rows into view rather than
// running off the bottom of it.
func TestMovingThroughTheListScrollsIt(t *testing.T) {
	m := newTestModel(t)
	all := crowd(t, m, 9)
	m.setFocus(focusSessions)

	// A title wraps, so "is it on screen" is asked of the rows the session
	// owns rather than of the text, which the wrap has cut in half.
	onScreen := func(want int) bool {
		full := m.sessionLines(max(4, m.sideW-2))
		drawn := len(strings.Split(m.sessionsPane(), "\n"))
		for i := m.sessTop; i < len(full) && i < m.sessTop+drawn; i++ {
			if full[i].idx == want {
				return true
			}
		}
		return false
	}

	m.sessSel = m.indexOf(all[0]) // the bottom of the list
	if !onScreen(m.sessSel) {
		t.Error("the last conversation is off the bottom when the cursor is on it")
	}
	m.sessSel = m.indexOf(all[len(all)-1]) // back to the top
	if !onScreen(m.sessSel) {
		t.Error("moving back to the top did not scroll back")
	}
}

// A click has to land on the row that was drawn, which after scrolling is not
// the row at the same index in the full list.
func TestAClickLandsOnTheRowItHitAfterScrolling(t *testing.T) {
	m := newTestModel(t)
	all := crowd(t, m, 9)
	m.mgr.Select(m.indexOf(all[0])) // the bottom of the list, so it must scroll
	m.setFocus(focusChat)

	drawn := strings.Split(stripANSI(m.sessionsPane()), "\n")
	if m.sessTop == 0 {
		t.Skip("the list did not need to scroll at this size")
	}
	full := m.sessionLines(max(4, m.sideW-2))

	top := headerRows + 1
	for row := range drawn {
		want := full[m.sessTop+row].idx
		if got := m.sessionRowAt(top + row); got != want {
			t.Errorf("row %d: a click lands on session %d, but %d was drawn there",
				row, got, want)
		}
	}
}
