package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/session"
)

func TestSlackExportHeadingsAndEmphasis(t *testing.T) {
	h, txt := slackExport("## Đề xuất\n\nchỉ cần **bấm hai lần**, _không_ ~~ba~~ lần, xem `E00503` và [PR](https://x.y/1).")
	for _, want := range []string{
		"<p><b>Đề xuất</b></p>", "<b>bấm hai lần</b>", "<i>không</i>", "<s>ba</s>",
		"<code>E00503</code>", `<a href="https://x.y/1">PR</a>`,
	} {
		if !strings.Contains(h, want) {
			t.Errorf("html is missing %q:\n%s", want, h)
		}
	}
	for _, want := range []string{"*Đề xuất*", "*bấm hai lần*", "_không_", "~ba~", "`E00503`", "<https://x.y/1|PR>"} {
		if !strings.Contains(txt, want) {
			t.Errorf("mrkdwn is missing %q:\n%s", want, txt)
		}
	}
	if strings.Contains(txt, "**") || strings.Contains(txt, "##") {
		t.Errorf("markdown syntax leaked into mrkdwn:\n%s", txt)
	}
}

// TestSlackExportTableIsAGrid: Slack has no tables, so one is sent as aligned
// monospaced text, measured in display columns.
func TestSlackExportTableIsAGrid(t *testing.T) {
	src := "| Trạng thái | Số |\n|---|---:|\n| **approved** | 786 |\n| 目視審査 | 38 |\n"
	h, txt := slackExport(src)
	if !strings.HasPrefix(h, "<pre>") || strings.Contains(h, "<table") {
		t.Errorf("a table should paste as a code block:\n%s", h)
	}
	want := strings.Join([]string{
		"```",
		"Trạng thái |  Số",
		"-----------+----",
		"approved   | 786",
		"目視審査   |  38",
		"```",
	}, "\n")
	if txt != want {
		t.Errorf("grid:\n%s\nwant:\n%s", txt, want)
	}
}

func TestSlackExportNestedListsNest(t *testing.T) {
	h, txt := slackExport("- **BE**: tách\n  - con\n- FE\n\n1. một\n2. hai")
	wantHTML := "<ul><li><b>BE</b>: tách<ul><li>con</li></ul></li><li>FE</li></ul><ol><li>một</li><li>hai</li></ol>"
	if h != wantHTML {
		t.Errorf("html\n got %s\nwant %s", h, wantHTML)
	}
	wantText := "• *BE*: tách\n    ◦ con\n• FE\n\n1. một\n2. hai"
	if txt != wantText {
		t.Errorf("mrkdwn\n got %q\nwant %q", txt, wantText)
	}
}

func TestSlackExportCodeIsEscapedNotFormatted(t *testing.T) {
	h, txt := slackExport("```go\nif a < b && **x** {}\n```")
	if h != "<pre>if a &lt; b &amp;&amp; **x** {}</pre>" {
		t.Errorf("html = %s", h)
	}
	if txt != "```\nif a < b && **x** {}\n```" {
		t.Errorf("mrkdwn = %q", txt)
	}
}

// TestSlackBoldAgainstALetter: Slack reads an asterisk as bold only at a word
// boundary, which Vietnamese and Japanese text rarely leaves around it.
func TestSlackBoldAgainstALetter(t *testing.T) {
	_, txt := slackExport("tách**目視審査**ra")
	if txt != "tách"+zwsp+"*目視審査*"+zwsp+"ra" {
		t.Errorf("got %q", txt)
	}
	_, spaced := slackExport("tách **x** ra")
	if strings.Contains(spaced, zwsp) {
		t.Error("an invisible space was added where none is needed")
	}
}

func TestSlackExportQuoteAndRule(t *testing.T) {
	h, txt := slackExport("> một\n> **hai**\n\n---\n\nsau")
	if !strings.Contains(h, "<blockquote>một<br><b>hai</b></blockquote>") {
		t.Errorf("html = %s", h)
	}
	if !strings.HasPrefix(txt, "> một\n> *hai*\n\n─") {
		t.Errorf("mrkdwn = %q", txt)
	}
}

// ---- in the UI -------------------------------------------------------------

func addTurn(m *Model, prompt string, replies ...session.Message) {
	s := m.mgr.Active()
	s.Append(session.Message{Role: session.RoleUser, Text: prompt, At: time.Now()})
	for _, r := range replies {
		r.Role = session.RoleAssistant
		s.Append(r)
	}
	m.invalidateChat()
	m.View()
}

func TestLastReplySkipsToolOnlyMessages(t *testing.T) {
	m := newTestModel(t)
	addTurn(m, "điều tra bug",
		session.Message{Text: "## Kết luận\n\nđây"},
		session.Message{Tools: []session.ToolCall{{Name: "Bash"}}},
	)
	if got := lastReply(m.mgr.Active()); got != "## Kết luận\n\nđây" {
		t.Errorf("last reply = %q", got)
	}
}

// TestCopyConvertsOnlyWhenRun: asking to copy hands back a command and does
// no conversion of its own — the work happens when the command runs, off the
// path that draws frames.
func TestCopyConvertsOnlyWhenRun(t *testing.T) {
	m := newTestModel(t)
	if cmd := m.runSlash("copy", ""); cmd != nil {
		t.Error("with no answer there should be nothing to run")
	}
	addTurn(m, "q", session.Message{Text: "**đáp**"})
	if cmd := m.runSlash("copy", ""); cmd == nil {
		t.Fatal("/copy did nothing")
	}
	if !strings.Contains(m.notice, "for Slack") {
		t.Errorf("notice = %q", m.notice)
	}

	cmd := m.slackCopied(slackCopiedMsg{what: "x", text: "*đáp*", err: errTest("no tool")})
	if cmd == nil || !strings.Contains(m.notice, "through the terminal") {
		t.Errorf("an unreachable clipboard should fall back to the terminal: %q", m.notice)
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

func TestYCopiesTheAnswerInView(t *testing.T) {
	m := newTestModel(t)
	long := strings.Repeat("dòng\n\n", 60)
	addTurn(m, "first", session.Message{Text: "FIRST " + long})
	addTurn(m, "second", session.Message{Text: "SECOND answer"})

	m.showMessage(1) // the first answer fills the pane
	if got := m.replyInView(); !strings.HasPrefix(got, "FIRST") {
		t.Errorf("in view at the first answer, got %.20q", got)
	}
	// Scrolled to the bottom, the top line may be the tail of the first
	// answer, but the one being read is the second.
	addTurn(m, "third", session.Message{Text: "THIRD " + long})
	m.showMessage(4)
	if got := m.replyInView(); !strings.HasPrefix(got, "THIRD") {
		t.Errorf("in view at the third answer, got %.20q", got)
	}
}
