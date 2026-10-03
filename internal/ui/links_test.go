package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/phanngoc/agent-tui/internal/session"
)

// ---- finding what a click points at ----------------------------------------

func TestFindLinkAt(t *testing.T) {
	src := "Created and validated:\n\n" +
		"- [SKILL.md](/home/me/ws/skills/brainstorm/SKILL.md)\n" +
		"- [Correctness **checklist**](/home/me/ws/skills/brainstorm/references/checklist.md)\n" +
		"See `ekyc.service.ts:256` and skills/brainstorm-business-logic, or https://x.y/z."
	for _, tc := range []struct {
		name, line, at string // at: the text the click lands on
		want           linkTarget
		ok             bool
	}{
		{"link text", "• SKILL.md (/home/me/ws/skills/brainstorm/SKILL.md)", "SKILL",
			linkTarget{path: "/home/me/ws/skills/brainstorm/SKILL.md"}, true},
		{"link with emphasis in its text", "• Correctness checklist (/home/me/ws/skills/br…)", "checklist",
			linkTarget{path: "/home/me/ws/skills/brainstorm/references/checklist.md"}, true},
		{"the shortened URL after it, cut by the line", "• Correctness checklist (/home/me/ws/skills/br…)", "skills/br",
			linkTarget{path: "/home/me/ws/skills/brainstorm/references/checklist.md"}, true},
		{"a path with a line", "See ekyc.service.ts:256 and skills/brainstorm-business-logic,", "service",
			linkTarget{path: "ekyc.service.ts", line: 256}, true},
		{"a bare directory-ish path, comma trimmed", "See ekyc.service.ts:256 and skills/brainstorm-business-logic,", "business",
			linkTarget{path: "skills/brainstorm-business-logic"}, true},
		{"a web address", "or https://x.y/z.", "x.y",
			linkTarget{path: "https://x.y/z"}, true},
		{"an ordinary word", "See ekyc.service.ts:256 and skills", "and", linkTarget{}, false},
		{"blank space", "See ekyc.service.ts:256 and skills", " ", linkTarget{}, false},
	} {
		col := ansi.StringWidth(tc.line[:strings.Index(tc.line, tc.at)])
		got, ok := findLinkAt(tc.line, col, src)
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s: got %+v ok=%v, want %+v ok=%v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

func TestSplitLine(t *testing.T) {
	for in, want := range map[string]linkTarget{
		"a.go:42":                {path: "a.go", line: 42},
		"a.go:42:7":              {path: "a.go", line: 42},
		"src/a.ts#L10-L20":       {path: "src/a.ts", line: 10},
		"file:///home/me/x.md":   {path: "/home/me/x.md"},
		`C:\Users\me\a.go`:       {path: `C:\Users\me\a.go`},
		`C:\Users\me\a.go:12`:    {path: `C:\Users\me\a.go`, line: 12},
		"/home/me/no-line-here/": {path: "/home/me/no-line-here/"},
	} {
		if got := splitLine(in); got != want {
			t.Errorf("%q: got %+v, want %+v", in, got, want)
		}
	}
}

func TestWordAtColHandlesWideText(t *testing.T) {
	line := "日本語 ekyc.service.ts:256 です"
	col := ansi.StringWidth("日本語 ekyc")
	if got := wordAtCol(line, col); got != "ekyc.service.ts:256" {
		t.Errorf("got %q", got)
	}
}

// ---- in the transcript -------------------------------------------------------

// agentSays puts an answer in the transcript and draws it.
func agentSays(m *Model, text string) {
	s := m.mgr.Active()
	s.Append(session.Message{Role: session.RoleUser, Text: "make it", At: time.Now()})
	s.Append(session.Message{Role: session.RoleAssistant, Text: text, At: time.Now()})
	m.invalidateChat()
	m.View()
}

// screenAt finds where some text is drawn in the transcript.
func screenAt(t *testing.T, m *Model, text string) (int, int) {
	t.Helper()
	left, top, _, h, ok := m.paneBox(focusChat)
	if !ok {
		t.Fatal("no transcript pane")
	}
	lines := strings.Split(m.chatSet, "\n")
	for r := 0; r < h; r++ {
		i := m.chat.YOffset() + r
		if i >= len(lines) {
			break
		}
		plain := ansi.Strip(lines[i])
		if at := strings.Index(plain, text); at >= 0 {
			return left + ansi.StringWidth(plain[:at]) + 1, top + r
		}
	}
	t.Fatalf("%q is not on screen", text)
	return 0, 0
}

func ctrlClick(m *Model, x, y int) tea.Cmd {
	return m.onMouse(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft, Mod: tea.ModCtrl})
}

// follow runs a command and the commands its results lead to, as the program
// would, until there is nothing left to do.
func follow(m *Model, cmd tea.Cmd) {
	for i := 0; cmd != nil && i < 6; i++ {
		msg := cmd()
		if msg == nil {
			return
		}
		_, cmd = m.Update(msg)
	}
}

func TestCtrlClickALinkOpensItInThePreview(t *testing.T) {
	m := newTestModel(t)
	agentSays(m, "Created:\n\n- [Core rules](internal/core/core.go)\n- `main.go:6` prints it")

	x, y := screenAt(t, m, "Core rules")
	follow(m, ctrlClick(m, x, y))
	if m.file == nil || m.file.Rel != "internal/core/core.go" {
		t.Fatalf("preview shows %+v; notice %q", m.file, m.notice)
	}

	x, y = screenAt(t, m, "main.go:6")
	follow(m, ctrlClick(m, x, y))
	if m.file == nil || m.file.Rel != "main.go" || m.fileLine != 6 {
		t.Errorf("preview shows %+v at line %d", m.file, m.fileLine)
	}
}

func TestCtrlClickOpensAClosedPreview(t *testing.T) {
	m := newTestModel(t)
	m.togglePreview()
	agentSays(m, "Wrote [the entry point](main.go).")
	x, y := screenAt(t, m, "the entry point")
	follow(m, ctrlClick(m, x, y))
	if !m.showPreview || m.file == nil || m.file.Rel != "main.go" {
		t.Errorf("preview open=%v file=%+v", m.showPreview, m.file)
	}
}

func TestCtrlClickSaysWhenThereIsNothingToOpen(t *testing.T) {
	m := newTestModel(t)
	agentSays(m, "Nothing here but words, and [a missing file](nope/missing.go).")

	x, y := screenAt(t, m, "words")
	follow(m, ctrlClick(m, x, y))
	if !strings.Contains(m.notice, "nothing to open") {
		t.Errorf("notice = %q", m.notice)
	}
	x, y = screenAt(t, m, "a missing file")
	follow(m, ctrlClick(m, x, y))
	if !strings.Contains(m.notice, "no file at") || m.file != nil {
		t.Errorf("notice = %q, file = %+v", m.notice, m.file)
	}
}

// TestPlainClickStillSelects: without ctrl, a click on a link is the start of
// a selection, as it always was.
func TestPlainClickStillSelects(t *testing.T) {
	m := newTestModel(t)
	agentSays(m, "Wrote [the entry point](main.go).")
	x, y := screenAt(t, m, "the entry point")
	follow(m, m.onMouse(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}))
	if m.file != nil {
		t.Error("a plain click opened the file")
	}
	if !m.sel.on {
		t.Error("a plain click did not start a selection")
	}
}
