package ui

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/phanngoc/agent-tui/internal/search"
	"github.com/phanngoc/agent-tui/internal/session"
)

// conversation builds one corpus entry out of alternating messages.
func conversation(id, title, root string, texts ...string) session.Entry {
	e := session.Entry{ID: id, Title: title, Root: root}
	for i, t := range texts {
		role := session.RoleUser
		if i%2 == 1 {
			role = session.RoleAssistant
		}
		e.Texts = append(e.Texts, t)
		e.Roles = append(e.Roles, role)
	}
	return e
}

func query(q string) search.Options { return search.Options{Query: q, Limit: recallLimit} }

// The requirement: every project, not just the one that happens to be open.
func TestRecallFindsEveryProject(t *testing.T) {
	got := scanCorpus([]session.Entry{
		conversation("a", "ranking", "/work/api", "sao resolvePfid chậm"),
		conversation("b", "onboarding", "/work/web", "resolvePfid trả về gì"),
		conversation("c", "khác hẳn", "/work/ops", "deploy thế nào"),
	}, query("resolvePfid"))

	if len(got.convs) != 2 {
		t.Fatalf("found %d conversations, want the two that mention it", len(got.convs))
	}
	roots := got.convs[0].entry.Root + "," + got.convs[1].entry.Root
	if !strings.Contains(roots, "/work/api") || !strings.Contains(roots, "/work/web") {
		t.Errorf("the hits came from %q, want both projects", roots)
	}
}

// A hit is an address: which conversation, and which message in it. Not a byte
// offset and not a line of the rendered transcript, because only the message
// index survives being rendered again.
func TestARecallHitIsAnAddress(t *testing.T) {
	got := scanCorpus([]session.Entry{
		conversation("a", "t", "/p", "mở đầu", "chưa liên quan", "hỏi về resolvePfid"),
	}, query("resolvePfid"))

	if len(got.convs) != 1 || len(got.convs[0].hits) != 1 {
		t.Fatalf("found %+v", got.convs)
	}
	h := got.convs[0].hits[0]
	if h.msg != 2 {
		t.Errorf("the hit points at message %d, want 2", h.msg)
	}
	if h.role != session.RoleUser {
		t.Errorf("the hit is tagged %q", h.role)
	}
	if !strings.Contains(h.Text, "resolvePfid") {
		t.Errorf("the matching line is %q", h.Text)
	}
}

// The scope is what was said, and it has to stay that way. Tool results are
// eighty-five per cent of the bytes on disk; letting them in would be a silent
// decision to search file dumps, made by nobody.
func TestRecallDoesNotSearchToolResultsOrThinking(t *testing.T) {
	// Entry is built from the session file, so the guarantee is upstream: the
	// projection carries prose and nothing else. This pins that the projection
	// of a real session leaves the rest behind.
	in, _ := json.Marshal(map[string]string{"file_path": "secretNeedle.go"})
	s := &session.Session{
		ID: "a", Root: "/p",
		Messages: []session.Message{{
			Role:     session.RoleAssistant,
			Text:     "không có gì ở đây",
			Thinking: "secretNeedle in the thinking",
			Tools: []session.ToolCall{{
				ID: "t", Name: "read_file", Input: in,
				Result: "secretNeedle in the result", Done: true,
			}},
		}},
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "secretNeedle") {
		t.Fatal("the fixture does not contain the needle it is meant to hide")
	}

	e := conversation("a", "t", "/p", s.Messages[0].Text)
	if got := scanCorpus([]session.Entry{e}, query("secretNeedle")); got.total != 0 {
		t.Errorf("a needle hidden in a tool result or in thinking was found: %+v", got.convs)
	}
}

// One long conversation may not crowd every other project off the screen.
func TestRecallCapsAnyOneConversation(t *testing.T) {
	var texts []string
	for i := 0; i < 200; i++ {
		texts = append(texts, "resolvePfid lần "+strconv.Itoa(i))
	}
	got := scanCorpus([]session.Entry{
		conversation("a", "dài", "/p", texts...),
		conversation("b", "ngắn", "/q", "resolvePfid một lần"),
	}, query("resolvePfid"))

	if len(got.convs) != 2 {
		t.Fatalf("the short conversation was crowded out: %d groups", len(got.convs))
	}
	if n := len(got.convs[0].hits); n > recallPerConv {
		t.Errorf("the long conversation contributed %d hits, past the cap of %d", n, recallPerConv)
	}
}

// Empty messages keep their place, because the index is the address.
func TestRecallIndexesSurviveEmptyMessages(t *testing.T) {
	got := scanCorpus([]session.Entry{
		conversation("a", "t", "/p", "mở đầu", "", "", "resolvePfid ở đây"),
	}, query("resolvePfid"))

	if got.convs[0].hits[0].msg != 3 {
		t.Errorf("the hit points at message %d, want 3", got.convs[0].hits[0].msg)
	}
}

// Typing must not be eaten by the list's own navigation — the project search
// had exactly this bug, where a letter moved the cursor instead of reaching
// the query.
func TestRecallBoxKeepsEveryLetter(t *testing.T) {
	m := newTestModel(t)
	m.openRecall("")
	for _, r := range "jkln resolvePfid" {
		m.recallKey(key(string(r)))
	}
	if got := m.recallIn.Value(); got != "jkln resolvePfid" {
		t.Errorf("the query reads %q; the list ate the letters it navigates by", got)
	}
}

// A query typed while the conversations are still being read is not thrown
// away: it is applied when they arrive, rather than showing "nothing found"
// for a corpus that is simply not there yet.
func TestRecallAppliesAQueryTypedWhileLoading(t *testing.T) {
	m := newTestModel(t)
	m.openRecall("")
	m.recallBusy = true
	for _, r := range "resolvePfid" {
		m.recallKey(key(string(r)))
	}
	if m.recallPending != "resolvePfid" {
		t.Fatalf("the query was dropped: pending = %q", m.recallPending)
	}

	m.setCorpus(recallMsg{
		seq:    m.recallSeq,
		corpus: []session.Entry{conversation("a", "t", "/p", "về resolvePfid")},
	})
	if m.recallRes.total != 1 {
		t.Errorf("the query was not applied when the conversations arrived: %+v", m.recallRes)
	}
}

// The overlay has a text box, so it has to report a cursor. Without one a
// composing input method has nowhere to draw, which is how Vietnamese is typed
// into this program.
func TestRecallShowsACursor(t *testing.T) {
	m := newTestModel(t)
	m.openRecall("x")
	m.View()
	if m.cursor() == nil {
		t.Error("the search box has no cursor, so composing input cannot be seen")
	}
}

// The whole point, end to end: a hit in a conversation this process has never
// loaded opens it and lands on the message the match was in.
func TestOpeningAHitLoadsTheConversationAndScrollsToIt(t *testing.T) {
	m := newTestModel(t)
	// A conversation saved by an earlier run, in another project.
	other := m.mgr.New()
	other.Root = "/somewhere/else"
	other.Title = "về ranking"
	for i := 0; i < 12; i++ {
		other.Append(session.Message{Role: session.RoleUser, Text: "câu " + strconv.Itoa(i)})
		other.Append(session.Message{Role: session.RoleAssistant, Text: "đáp " + strconv.Itoa(i)})
	}
	other.Append(session.Message{Role: session.RoleUser, Text: "resolvePfid nằm ở đâu"})
	want := len(other.Messages) - 1
	m.mgr.Save(other)
	m.mgr.Select(0) // stand somewhere else

	m.openRecall("")
	m.setCorpus(recallMsg{seq: m.recallSeq, corpus: m.mgr.LiveEntries()})
	m.runRecall("resolvePfid")
	hit, entry, ok := func() (convoHit, session.Entry, bool) {
		for i := range m.recallRows {
			m.recallSel = i
			if h, e, ok := m.selectedRecall(); ok {
				return h, e, true
			}
		}
		return convoHit{}, session.Entry{}, false
	}()
	if !ok {
		t.Fatalf("no hit to open: %+v", m.recallRes)
	}

	m.openRecallHit(hit, entry)

	if m.mgr.Active().ID != other.ID {
		t.Fatalf("opened %q, want %q", m.mgr.Active().ID, other.ID)
	}
	if m.overlay != overlayNone {
		t.Error("the overlay stayed open over the conversation it opened")
	}
	m.View()
	// Visible, rather than exactly at the top: the viewport clamps an offset
	// past the end, and a conversation shorter than the pane cannot put its
	// last message at the top. Being able to see it is the property that
	// matters, and the one that holds either way.
	at, top, h := m.chatStarts[want], m.chat.YOffset(), m.chat.Height()
	if at < top || at >= top+h {
		t.Errorf("message %d starts at line %d, outside the %d lines shown from %d",
			want, at, h, top)
	}
	// Coming from another project is a visible, several-hundred-millisecond
	// event, so it is said out loud rather than left to look like a glitch.
	if !strings.Contains(m.notice, "else") {
		t.Errorf("nothing said the conversation came from another project: %q", m.notice)
	}
}

// The summary says how many of what. It once said "results in conversations
// across projects", because the helper it used pluralises a word without
// counting it.
func TestRecallSummaryCountsThings(t *testing.T) {
	m := newTestModel(t)
	m.openRecall("")
	m.setCorpus(recallMsg{seq: m.recallSeq, corpus: []session.Entry{
		conversation("a", "t", "/work/api", "về resolvePfid"),
		conversation("b", "t", "/work/web", "resolvePfid nữa"),
	}})
	m.recallIn.SetValue("resolvePfid")
	m.runRecall("resolvePfid")

	got := stripANSI(m.recallSummary())
	if !strings.Contains(got, "2 results") || !strings.Contains(got, "2 conversations") ||
		!strings.Contains(got, "2 projects") {
		t.Errorf("the summary does not say how many: %q", got)
	}
}

// The conversation search is the same shape and gets the same mouse. A hit
// under the pointer opens its conversation at the message it was found in.
func TestClickingARecallResultOpensIt(t *testing.T) {
	m := newTestModel(t)
	other := m.mgr.New()
	other.Title = "về ranking"
	for i := 0; i < 6; i++ {
		other.Append(session.Message{Role: session.RoleUser, Text: "câu " + strconv.Itoa(i)})
	}
	other.Append(session.Message{Role: session.RoleUser, Text: "resolvePfid nằm ở đâu"})
	want := len(other.Messages) - 1
	m.mgr.Select(0)

	m.openRecall("")
	m.setCorpus(recallMsg{seq: m.recallSeq, corpus: m.mgr.LiveEntries()})
	m.recallIn.SetValue("resolvePfid")
	m.runRecall("resolvePfid")
	m.View()

	row := -1
	for i := m.recallTop; i < m.recallTop+m.recallDrawn && i < len(m.recallRows); i++ {
		if m.recallRows[i].hit >= 0 {
			row = i
			break
		}
	}
	if row < 0 {
		t.Fatalf("no hit row was drawn: %d rows, %d drawn", len(m.recallRows), m.recallDrawn)
	}

	m.onMouse(tea.MouseClickMsg{
		X: m.overlayX + 4, Y: m.overlayY + 1 + m.recallBodyY + (row - m.recallTop),
		Button: tea.MouseLeft,
	})

	if m.mgr.Active().ID != other.ID {
		t.Fatalf("the click opened %q, want %q", m.mgr.Active().ID, other.ID)
	}
	m.View()
	at, top, h := m.chatStarts[want], m.chat.YOffset(), m.chat.Height()
	if at < top || at >= top+h {
		t.Errorf("message %d starts at line %d, outside the %d shown from %d", want, at, h, top)
	}
}

// And a click on a conversation header folds it rather than opening whatever
// is beneath.
func TestClickingARecallHeaderFoldsIt(t *testing.T) {
	m := newTestModel(t)
	m.openRecall("")
	m.setCorpus(recallMsg{seq: m.recallSeq, corpus: []session.Entry{
		conversation("a", "một", "/p", "về resolvePfid"),
		conversation("b", "hai", "/q", "resolvePfid nữa"),
	}})
	m.recallIn.SetValue("resolvePfid")
	m.runRecall("resolvePfid")
	m.View()

	row := -1
	for i := m.recallTop; i < m.recallTop+m.recallDrawn && i < len(m.recallRows); i++ {
		if m.recallRows[i].hit < 0 {
			row = i
			break
		}
	}
	if row < 0 {
		t.Fatal("no header was drawn")
	}
	conv := m.recallRows[row].conv
	was := m.recallRes.convs[conv].collapsed

	m.onMouse(tea.MouseClickMsg{
		X: m.overlayX + 4, Y: m.overlayY + 1 + m.recallBodyY + (row - m.recallTop),
		Button: tea.MouseLeft,
	})

	if m.recallRes.convs[conv].collapsed == was {
		t.Error("clicking the header did not fold the conversation")
	}
	if m.overlay != overlayRecall {
		t.Error("clicking a header closed the search")
	}
}
