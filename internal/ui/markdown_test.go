package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/phanngoc/agent-tui/internal/highlight"
	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/theme"
)

func mdFixture() (*theme.Styles, *highlight.Scheme) {
	st := theme.New(theme.ByName(""))
	sc := highlight.NewScheme(st.P.Fg, st.P.Keyword, st.P.Type, st.P.String,
		st.P.Number, st.P.Comment, st.P.Func, st.P.Punct)
	return st, sc
}

// mdPlain renders src and strips the styling, which is what the reader's eye
// is left with once the colours are taken away.
func mdPlain(t *testing.T, src string, w int) string {
	t.Helper()
	st, sc := mdFixture()
	lines := renderMarkdown(st, sc, src, w)
	for i, l := range lines {
		if got := lipgloss.Width(l); got > w {
			t.Errorf("line %d is %d cells wide, want at most %d: %q", i, got, w, stripANSI(l))
		}
	}
	return stripANSI(strings.Join(lines, "\n"))
}

// TestMarkdownLeavesNoSyntaxBehind is the whole point of the renderer: the
// reader should see the document, never the markup that describes it.
func TestMarkdownLeavesNoSyntaxBehind(t *testing.T) {
	src := strings.Join([]string{
		"## Host hạ tầng",
		"",
		"| Host | Vai trò |",
		"|---|---|",
		"| mail.fpaas-dev | SES domain |",
		"",
		"Quy tắc: **line 2 luôn có hậu tố `-spa`**.",
		"",
		"```go",
		"func main() {}",
		"```",
		"",
		"- một",
		"- hai",
	}, "\n")

	out := mdPlain(t, src, 60)
	for _, syntax := range []string{"##", "**", "```", "|---|", "| Host |"} {
		if strings.Contains(out, syntax) {
			t.Errorf("markdown syntax %q survived into the output:\n%s", syntax, out)
		}
	}
	for _, want := range []string{"HOST HẠ TẦNG", "mail.fpaas-dev", "SES domain",
		"line 2 luôn có hậu tố -spa", "func main() {}", "• một"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in the output:\n%s", want, out)
		}
	}
}

func TestMarkdownHeadings(t *testing.T) {
	out := mdPlain(t, "# Tổng quan\n\n### Chi tiết\n\nthân bài", 40)
	if !strings.Contains(out, "▌TỔNG QUAN") {
		t.Errorf("a top-level heading should get a rail and weight:\n%s", out)
	}
	// Deep headings keep their case: they are closer to body text than to a
	// title, and upper case would shout over the section they sit in.
	if !strings.Contains(out, "Chi tiết") {
		t.Errorf("a level-3 heading should keep its case:\n%s", out)
	}
}

// A heading that names code is left in its own case: an identifier is not the
// same word upper-cased.
func TestMarkdownHeadingKeepsCodeCase(t *testing.T) {
	out := mdPlain(t, "## The `renderMarkdown` entry point", 60)
	if !strings.Contains(out, "renderMarkdown") {
		t.Errorf("a heading naming code should keep its case:\n%s", out)
	}
}

func TestMarkdownTableAligns(t *testing.T) {
	src := strings.Join([]string{
		"| Host | Vai trò | Cổng |",
		"|:-----|:--------|-----:|",
		"| api | backend | 3000 |",
		"| view-spa | frontend | 443 |",
	}, "\n")
	out := mdPlain(t, src, 60)
	lines := strings.Split(out, "\n")
	if len(lines) < 4 {
		t.Fatalf("want a header, a rule and two rows:\n%s", out)
	}
	if !strings.HasPrefix(lines[1], "─") {
		t.Errorf("the header should be underlined by a rule:\n%s", out)
	}
	// Both rows put "backend" and "frontend" in the same column, which is the
	// only reason to render a table rather than the raw pipes.
	at := func(l, cell string) int { return strings.Index(l, cell) }
	if a, b := at(lines[2], "backend"), at(lines[3], "frontend"); a != b {
		t.Errorf("column 2 starts at %d then %d:\n%s", a, b, out)
	}
	// A right-aligned column ends where the widest of its cells ends.
	if !strings.HasSuffix(strings.TrimRight(lines[2], " "), "3000") {
		t.Errorf("the right-aligned column should end flush:\n%s", out)
	}
}

// A table wider than the pane is squeezed rather than allowed to wrap into
// nonsense: the alignment is what carries the meaning.
func TestMarkdownTableFitsNarrowPane(t *testing.T) {
	src := strings.Join([]string{
		"| Host | Vai trò |",
		"|---|---|",
		"| mail.fpaas-dev.sun-asterisk.vn | SES domain identity with a long note |",
	}, "\n")
	out := mdPlain(t, src, 30)
	for _, l := range strings.Split(out, "\n") {
		if lipgloss.Width(l) > 30 {
			t.Fatalf("line %q is wider than the pane", l)
		}
	}
	if !strings.Contains(out, "…") {
		t.Errorf("a squeezed cell should be elided, not dropped:\n%s", out)
	}
}

func TestMarkdownLists(t *testing.T) {
	src := strings.Join([]string{
		"- đầu tiên",
		"  - lồng nhau",
		"1. một",
		"2. hai",
		"- [x] xong",
		"- [ ] chưa",
	}, "\n")
	out := mdPlain(t, src, 40)
	for _, want := range []string{"• đầu tiên", "  ◦ lồng nhau", "1. một", "2. hai", "☑ xong", "☐ chưa"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in the output:\n%s", want, out)
		}
	}
}

// A wrapped item hangs under its own text rather than under its bullet, so the
// bullet column stays readable as a column.
func TestMarkdownListHangingIndent(t *testing.T) {
	out := mdPlain(t, "- "+strings.Repeat("từ ", 20), 30)
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		t.Fatalf("the item should have wrapped:\n%s", out)
	}
	if !strings.HasPrefix(lines[1], "  ") || strings.HasPrefix(lines[1], "   ") {
		t.Errorf("the continuation should start in the text column, got %q", lines[1])
	}
}

func TestMarkdownCodeBlock(t *testing.T) {
	src := "Trước:\n\n```go\nfunc main() {\n\tprintln(\"hi\")\n}\n```\n\nSau."
	st, sc := mdFixture()
	lines := renderMarkdown(st, sc, src, 60)

	var code []string
	for _, l := range lines {
		if strings.Contains(stripANSI(l), "func main") || strings.Contains(stripANSI(l), "println") {
			code = append(code, l)
		}
	}
	if len(code) != 2 {
		t.Fatalf("want the two code lines, got %d:\n%s", len(code), stripANSI(strings.Join(lines, "\n")))
	}
	for _, l := range code {
		if !strings.HasPrefix(stripANSI(l), "  ") {
			t.Errorf("code should be indented, got %q", stripANSI(l))
		}
		if !strings.Contains(l, "\x1b[") {
			t.Errorf("code should be highlighted, got %q", l)
		}
	}
}

// Half-written markdown arrives on every streaming delta, so an unclosed fence
// has to render what it has instead of swallowing the rest of the answer.
func TestMarkdownUnclosedFenceStillRenders(t *testing.T) {
	out := mdPlain(t, "Đang chạy:\n\n```sh\nmake test\n", 40)
	if !strings.Contains(out, "make test") {
		t.Errorf("an unfinished code block should still show its lines:\n%s", out)
	}
	if strings.Contains(out, "```") {
		t.Errorf("the fence itself should not be shown:\n%s", out)
	}
}

func TestMarkdownInline(t *testing.T) {
	st, sc := mdFixture()
	out := strings.Join(renderMarkdown(st, sc, "**đậm**, *nghiêng*, `mã`, ~~bỏ~~", 60), "\n")
	plain := stripANSI(out)
	if plain != "đậm, nghiêng, mã, bỏ" {
		t.Errorf("markers should be gone, got %q", plain)
	}
	// Bold arrives as the SGR attribute next to the colour, not as asterisks.
	if !strings.Contains(out, "\x1b[1;") {
		t.Errorf("bold should become an attribute, not asterisks: %q", out)
	}
}

// Identifiers are not emphasis. An underscore inside a word is part of the
// word, and a reply about code is full of them.
func TestMarkdownLeavesIdentifiersAlone(t *testing.T) {
	out := mdPlain(t, "Gọi do_the_thing và MAX_RETRY_COUNT.", 60)
	if !strings.Contains(out, "do_the_thing") || !strings.Contains(out, "MAX_RETRY_COUNT") {
		t.Errorf("underscores inside words should survive: %q", out)
	}
}

func TestMarkdownLinkKeepsItsTarget(t *testing.T) {
	out := mdPlain(t, "Xem [tài liệu](https://example.com/docs).", 60)
	if !strings.Contains(out, "tài liệu") {
		t.Errorf("link text missing: %q", out)
	}
	// Nothing is clickable in a terminal, so a hidden URL is a lost one.
	if !strings.Contains(out, "https://example.com/docs") {
		t.Errorf("link target missing: %q", out)
	}
}

func TestMarkdownQuoteAndRule(t *testing.T) {
	out := mdPlain(t, "> lưu ý điều này\n\n---\n\nsau đó", 40)
	if !strings.Contains(out, "│ lưu ý điều này") {
		t.Errorf("a quote should get a rail:\n%s", out)
	}
	if !strings.Contains(out, strings.Repeat("─", 40)) {
		t.Errorf("a thematic break should become a rule:\n%s", out)
	}
}

// Plain prose is the common case and must come through untouched.
func TestMarkdownPlainProse(t *testing.T) {
	out := mdPlain(t, "Câu đầu tiên.\nCâu thứ hai.\n\nĐoạn sau.", 60)
	if out != "Câu đầu tiên. Câu thứ hai.\n\nĐoạn sau." {
		t.Errorf("prose should survive as prose, got %q", out)
	}
}

func TestMarkdownBreaksLongWords(t *testing.T) {
	mdPlain(t, "Xem https://example.com/"+strings.Repeat("a", 90), 30)
}

// The transcript renders what the agent says, but shows what the user typed
// exactly as they typed it — their asterisks are theirs.
func TestTranscriptRendersAgentMarkdownOnly(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	s.Messages = append(s.Messages,
		session.Message{Role: session.RoleUser, Text: "## không phải heading"},
		session.Message{Role: session.RoleAssistant, Text: "## Kết quả\n\n- xong"},
	)
	m.chatKey = ""

	out := stripANSI(m.transcript(80))
	if !strings.Contains(out, "## không phải heading") {
		t.Errorf("the user's own text should be left alone:\n%s", out)
	}
	if strings.Contains(out, "## Kết quả") {
		t.Errorf("the agent's heading should have been rendered:\n%s", out)
	}
	if !strings.Contains(out, "KẾT QUẢ") || !strings.Contains(out, "• xong") {
		t.Errorf("want the rendered heading and bullet:\n%s", out)
	}
}
