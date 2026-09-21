package ui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/phanngoc/agent-tui/internal/session"
)

// turnOf builds a turn: a question, then one step per command, then an answer.
// The steps are separate messages because that is what they are — one round
// trip each — and folding has to work across them rather than within one.
func turnOf(m *Model, question string, n int, answer string) {
	s := m.mgr.Active()
	s.Messages = append(s.Messages, session.Message{Role: session.RoleUser, Text: question})
	for i := range n {
		s.Messages = append(s.Messages, step("", "cat file"+strconv.Itoa(i)+".ts"))
	}
	s.Messages = append(s.Messages, step(answer))
	m.invalidateChat()
}

// A turn keeps its last few calls. The rest already did their work while the
// turn was running, and what they leave behind is a wall between the question
// and the answer to it.
func TestALongTurnFoldsItsOlderCalls(t *testing.T) {
	m := newTestModel(t)
	turnOf(m, "đánh giá pipeline", 20, "Xong.")

	out := stripANSI(m.transcript(80))
	if n := strings.Count(out, "Bash"); n != callsShown {
		t.Errorf("%d calls are shown, want the last %d:\n%s", n, callsShown, out)
	}
	if !strings.Contains(out, "… 15 earlier calls") {
		t.Errorf("the fold does not say what it hid:\n%s", out)
	}
	// The ones kept are the last ones: they are what the answer is about to
	// refer to.
	for i := 15; i < 20; i++ {
		if !strings.Contains(out, "file"+strconv.Itoa(i)+".ts") {
			t.Errorf("call %d should have survived the fold:\n%s", i, out)
		}
	}
	if strings.Contains(out, "file0.ts") {
		t.Errorf("the first call is still on screen:\n%s", out)
	}
	// The question and the answer are still the two ends of one block.
	if !strings.Contains(out, "đánh giá pipeline") || !strings.Contains(out, "Xong.") {
		t.Errorf("the fold swallowed the conversation:\n%s", out)
	}
}

// Nothing is thrown away; alt+o brings it back.
func TestAltOUnfoldsAndFoldsAgain(t *testing.T) {
	m := newTestModel(t)
	turnOf(m, "đánh giá", 20, "Xong.")

	m.onKey(key("alt+o"))
	out := stripANSI(m.transcript(80))
	if n := strings.Count(out, "Bash"); n != 20 {
		t.Errorf("%d of 20 calls came back:\n%s", n, out)
	}
	if strings.Contains(out, "earlier calls") {
		t.Errorf("the fold line is still there when nothing is folded:\n%s", out)
	}

	m.onKey(key("alt+o"))
	if n := strings.Count(stripANSI(m.transcript(80)), "Bash"); n != callsShown {
		t.Errorf("folding again left %d calls", n)
	}
}

// A turn short enough to read is left alone, and so is one where folding would
// hide a single call: the line that says so costs as much as the line it hides.
func TestAShortTurnIsNotFolded(t *testing.T) {
	for _, n := range []int{1, callsShown, callsShown + 1} {
		m := newTestModel(t)
		turnOf(m, "hỏi", n, "đáp")

		out := stripANSI(m.transcript(80))
		if got := strings.Count(out, "Bash"); got != n {
			t.Errorf("a turn of %d calls shows %d:\n%s", n, got, out)
		}
		if strings.Contains(out, "earlier calls") {
			t.Errorf("a turn of %d calls was folded:\n%s", n, out)
		}
	}
}

// Each turn keeps its own last few. Folding the transcript as a whole would
// leave the older turns as headings with nothing under them.
func TestTurnsFoldIndependently(t *testing.T) {
	m := newTestModel(t)
	turnOf(m, "một", 12, "xong một")
	turnOf(m, "hai", 12, "xong hai")

	out := stripANSI(m.transcript(80))
	if n := strings.Count(out, "earlier calls"); n != 2 {
		t.Errorf("%d turns were folded, want 2:\n%s", n, out)
	}
	if n := strings.Count(out, "Bash"); n != 2*callsShown {
		t.Errorf("%d calls shown across two turns, want %d", n, 2*callsShown)
	}
	for _, want := range []string{"xong một", "xong hai"} {
		if !strings.Contains(out, want) {
			t.Errorf("an answer went missing:\n%s", out)
		}
	}
}

// The fold is planned over the run of steps, not over each message, so a turn
// whose calls are spread one per message folds as one thing.
func TestFoldSpansTheWholeRun(t *testing.T) {
	msgs := []session.Message{{Role: session.RoleUser, Text: "q"}}
	for range 8 {
		msgs = append(msgs, step("", "ls"))
	}
	plans := planCalls(msgs, 5, false)

	var shown, noted int
	for i, p := range plans {
		shown += len(msgs[i].Tools) - p.skip
		noted += p.note
	}
	if shown != 5 {
		t.Errorf("the plan shows %d calls, want 5", shown)
	}
	if noted != 3 {
		t.Errorf("the plan reports %d folded, want 3", noted)
	}
	// And it is reported once, on the message where the first surviving call
	// is, not on every message that lost one.
	count := 0
	for _, p := range plans {
		if p.note > 0 {
			count++
		}
	}
	if count != 1 {
		t.Errorf("the fold was announced %d times", count)
	}
}
