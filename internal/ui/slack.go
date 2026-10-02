package ui

import (
	"html"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

// An answer, made ready to paste into Slack.
//
// The transcript is where an investigation happens and Slack is where it gets
// passed on, and the trip between the two used to lose everything: copied as
// markdown, the headings arrived as "##", the tables as rows of pipes, and the
// bold as asterisks in pairs — Slack's own markup uses one.
//
// So the copy carries two things. HTML, which Slack's composer turns into its
// own formatting when it is pasted — bold, lists, links, quotes and code
// blocks come through as themselves. And, for anywhere that takes only text,
// the same thing in Slack's mrkdwn, which reads well as plain text too.
//
// Slack has no tables and no headings. A heading becomes a bold line. A table
// becomes a code block with its columns aligned, because a monospaced grid is
// the one table every Slack client shows the same way.
//
// The walk over the blocks is the transcript renderer's, using the same line
// classifiers, so what is pasted is split into paragraphs, lists and tables
// exactly where the reader saw them split.

// slackExport renders markdown as Slack-ready HTML and as mrkdwn text.
func slackExport(src string) (htmlOut, text string) {
	var h htmlOutW
	var t textOutW
	for _, b := range slackBlocks(mdSplit(src)) {
		h.block(b)
		t.block(b)
	}
	h.closeLists()
	return h.b.String(), strings.TrimRight(t.b.String(), "\n")
}

type sbKind int

const (
	sbPara sbKind = iota
	sbHead
	sbRule
	sbCode
	sbTable
	sbQuote
	sbItem
)

type sBlock struct {
	kind  sbKind
	text  string   // para, head, item
	lines []string // code body, quote body
	item  mdItem
	head  []string
	rows  [][]string
	align []int
}

// slackBlocks splits lines into blocks the way mdDoc.run does.
func slackBlocks(lines []string) []sBlock {
	var out []sBlock
	for i := 0; i < len(lines); {
		l := lines[i]
		switch {
		case strings.TrimSpace(l) == "":
			i++
		case mdFenceLen(l) > 0:
			ind, t := mdIndent(l)
			ch, n := t[0], mdFenceLen(l)
			j := i + 1
			var body []string
			for ; j < len(lines); j++ {
				if mdIsCloser(lines[j], ch, n) {
					j++
					break
				}
				body = append(body, mdUnindent(lines[j], ind))
			}
			for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
				body = body[:len(body)-1]
			}
			if len(body) > 0 {
				out = append(out, sBlock{kind: sbCode, lines: body})
			}
			i = j
		case mdHeadLevel(l) > 0:
			lv := mdHeadLevel(l)
			_, t := mdIndent(l)
			text := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(t[lv:]), "#"))
			out = append(out, sBlock{kind: sbHead, text: text})
			i++
		case mdIsRule(l):
			out = append(out, sBlock{kind: sbRule})
			i++
		case mdTableAt(lines, i):
			head := mdCells(lines[i])
			b := sBlock{kind: sbTable, head: head, align: mdAligns(lines[i+1], len(head))}
			j := i + 2
			for ; j < len(lines); j++ {
				if strings.TrimSpace(lines[j]) == "" || !strings.Contains(lines[j], "|") {
					break
				}
				b.rows = append(b.rows, mdCells(lines[j]))
			}
			out = append(out, b)
			i = j
		case mdIsQuote(l):
			var body []string
			for ; i < len(lines) && mdIsQuote(lines[i]); i++ {
				_, t := mdIndent(lines[i])
				body = append(body, strings.TrimPrefix(strings.TrimPrefix(t, ">"), " "))
			}
			out = append(out, sBlock{kind: sbQuote, lines: body})
		case mdIsItem(l):
			it, _ := mdItemAt(l)
			i++
			for i < len(lines) {
				ind, t := mdIndent(lines[i])
				if strings.TrimSpace(t) == "" || ind <= it.indent ||
					mdIsItem(lines[i]) || mdFenceLen(lines[i]) > 0 {
					break
				}
				it.text += " " + strings.TrimSpace(t)
				i++
			}
			out = append(out, sBlock{kind: sbItem, item: it, text: it.text})
		default:
			var parts []string
			j := i
			heading := false
			for ; j < len(lines); j++ {
				l := lines[j]
				if strings.TrimSpace(l) == "" {
					break
				}
				if j > i {
					if lv := mdSetext(l); lv > 0 && j == i+1 {
						heading = true
						j++
						break
					}
					if mdHeadLevel(l) > 0 || mdFenceLen(l) > 0 || mdIsRule(l) ||
						mdIsQuote(l) || mdIsItem(l) || mdTableAt(lines, j) {
						break
					}
				}
				parts = append(parts, strings.TrimSpace(l))
			}
			if heading {
				out = append(out, sBlock{kind: sbHead, text: parts[0]})
			} else {
				out = append(out, sBlock{kind: sbPara, text: strings.Join(parts, " ")})
			}
			i = j
		}
	}
	return out
}

// ---- inline ----------------------------------------------------------------

// inlineFmt is what one output does with each inline construct. glued says an
// emphasis run touches a letter on either side, which mrkdwn needs: Slack
// ignores an asterisk glued to a letter.
type inlineFmt interface {
	text(s string) string
	code(s string) string
	emph(n int, inner string, glued bool) string
	strike(inner string) string
	link(text, url string) string
}

// slackInline walks inline markdown the way mdDoc.inline does.
func slackInline(s string, f inlineFmt) string {
	var out, plain strings.Builder
	flush := func() {
		if plain.Len() > 0 {
			out.WriteString(f.text(plain.String()))
			plain.Reset()
		}
	}
	for i := 0; i < len(s); {
		switch s[i] {
		case '\\':
			if i+1 < len(s) && mdPunct(s[i+1]) {
				plain.WriteByte(s[i+1])
				i += 2
				continue
			}
		case '`':
			n := mdRun(s, i, '`')
			if end := mdFind(s, i+n, '`', n); end > 0 {
				flush()
				out.WriteString(f.code(strings.TrimSpace(s[i+n : end])))
				i = end + n
				continue
			}
		case '*', '_':
			if n, end, ok := mdEmph(s, i); ok {
				flush()
				glued := (i > 0 && wordish(s, i-1)) || (end+n < len(s) && wordish(s, end+n))
				out.WriteString(f.emph(n, slackInline(s[i+n:end], f), glued))
				i = end + n
				continue
			}
		case '~':
			if mdRun(s, i, '~') >= 2 {
				if end := mdFind(s, i+2, '~', 2); end > 0 {
					flush()
					out.WriteString(f.strike(slackInline(s[i+2:end], f)))
					i = end + 2
					continue
				}
			}
		case '!', '[':
			if text, url, n, ok := mdLink(s, i); ok {
				flush()
				out.WriteString(f.link(slackInline(text, f), url))
				i += n
				continue
			}
		case '<':
			if end := strings.IndexByte(s[i:], '>'); end > 1 && mdIsURL(s[i+1:i+end]) {
				flush()
				u := s[i+1 : i+end]
				out.WriteString(f.link(f.text(u), u))
				i += end + 1
				continue
			}
		}
		plain.WriteByte(s[i])
		i++
	}
	flush()
	return out.String()
}

// wordish reports whether the character around byte i is a letter or digit,
// counting every non-ASCII one: Vietnamese and Japanese letters glue to an
// asterisk as surely as English ones do.
func wordish(s string, i int) bool {
	if s[i] < utf8.RuneSelf {
		return mdWord(s[i]) && s[i] != '_'
	}
	return true
}

// ---- HTML ------------------------------------------------------------------

type htmlFmt struct{}

func (htmlFmt) text(s string) string { return html.EscapeString(s) }
func (htmlFmt) code(s string) string { return "<code>" + html.EscapeString(s) + "</code>" }
func (htmlFmt) emph(n int, inner string, _ bool) string {
	switch n {
	case 1:
		return "<i>" + inner + "</i>"
	case 2:
		return "<b>" + inner + "</b>"
	}
	return "<b><i>" + inner + "</i></b>"
}
func (htmlFmt) strike(inner string) string { return "<s>" + inner + "</s>" }
func (htmlFmt) link(text, url string) string {
	return `<a href="` + html.EscapeString(url) + `">` + text + "</a>"
}

type htmlList struct {
	ordered bool
	liOpen  bool
}

type htmlOutW struct {
	b     strings.Builder
	lists []htmlList
}

func (w *htmlOutW) closeTop() {
	top := w.lists[len(w.lists)-1]
	if top.liOpen {
		w.b.WriteString("</li>")
	}
	if top.ordered {
		w.b.WriteString("</ol>")
	} else {
		w.b.WriteString("</ul>")
	}
	w.lists = w.lists[:len(w.lists)-1]
}

func (w *htmlOutW) closeLists() {
	for len(w.lists) > 0 {
		w.closeTop()
	}
}

func (w *htmlOutW) item(it mdItem, text string) {
	level := min(it.indent/2, len(mdBullets)-1)
	for len(w.lists) > level+1 {
		w.closeTop()
	}
	if len(w.lists) == level+1 && w.lists[level].ordered != it.ordered {
		w.closeTop()
	}
	for len(w.lists) < level+1 {
		if n := len(w.lists); n > 0 && !w.lists[n-1].liOpen {
			w.b.WriteString("<li>")
			w.lists[n-1].liOpen = true
		}
		if it.ordered {
			w.b.WriteString("<ol>")
		} else {
			w.b.WriteString("<ul>")
		}
		w.lists = append(w.lists, htmlList{ordered: it.ordered})
	}
	top := &w.lists[len(w.lists)-1]
	if top.liOpen {
		w.b.WriteString("</li>")
	}
	w.b.WriteString("<li>" + itemCheck(it) + text)
	top.liOpen = true
}

func (w *htmlOutW) block(b sBlock) {
	if b.kind != sbItem {
		w.closeLists()
	}
	f := htmlFmt{}
	switch b.kind {
	case sbPara:
		w.b.WriteString("<p>" + slackInline(b.text, f) + "</p>")
	case sbHead:
		w.b.WriteString("<p><b>" + slackInline(b.text, f) + "</b></p>")
	case sbRule:
		w.b.WriteString("<p>" + strings.Repeat("─", 24) + "</p>")
	case sbCode:
		w.b.WriteString("<pre>" + html.EscapeString(strings.Join(b.lines, "\n")) + "</pre>")
	case sbTable:
		w.b.WriteString("<pre>" + html.EscapeString(slackGrid(b)) + "</pre>")
	case sbQuote:
		parts := make([]string, len(b.lines))
		for i, l := range b.lines {
			parts[i] = slackInline(l, f)
		}
		w.b.WriteString("<blockquote>" + strings.Join(parts, "<br>") + "</blockquote>")
	case sbItem:
		w.item(b.item, slackInline(b.text, f))
	}
}

func itemCheck(it mdItem) string {
	switch it.task {
	case mdTaskOpen:
		return "☐ "
	case mdTaskDone:
		return "☑ "
	}
	return ""
}

// ---- mrkdwn ----------------------------------------------------------------

type mrkdwnFmt struct{}

// zwsp lets a Slack marker sit against a letter: Slack only reads an asterisk
// as bold at a word boundary, and an invisible space makes one.
const zwsp = "​"

func (mrkdwnFmt) text(s string) string { return s }
func (mrkdwnFmt) code(s string) string { return "`" + s + "`" }
func (mrkdwnFmt) emph(n int, inner string, glued bool) string {
	var s string
	switch n {
	case 1:
		s = "_" + inner + "_"
	case 2:
		s = "*" + inner + "*"
	default:
		s = "*_" + inner + "_*"
	}
	if glued {
		s = zwsp + s + zwsp
	}
	return s
}
func (mrkdwnFmt) strike(inner string) string { return "~" + inner + "~" }
func (mrkdwnFmt) link(text, url string) string {
	if text == url {
		return url
	}
	return "<" + url + "|" + text + ">"
}

type textOutW struct {
	b        strings.Builder
	last     sbKind
	lastItem mdItem
	any      bool
}

func (w *textOutW) block(b sBlock) {
	// One blank line between blocks, none between the items of a list — but
	// a bulleted list running into a numbered one is two lists, and without
	// the line between them they read as one.
	sameList := b.kind == sbItem && w.last == sbItem &&
		!(b.item.indent == 0 && w.lastItem.indent == 0 && b.item.ordered != w.lastItem.ordered)
	if w.any && !sameList {
		w.b.WriteString("\n")
	}
	w.any, w.last, w.lastItem = true, b.kind, b.item

	f := mrkdwnFmt{}
	switch b.kind {
	case sbPara:
		w.b.WriteString(slackInline(b.text, f) + "\n")
	case sbHead:
		w.b.WriteString("*" + slackInline(b.text, f) + "*\n")
	case sbRule:
		w.b.WriteString(strings.Repeat("─", 24) + "\n")
	case sbCode:
		w.b.WriteString("```\n" + strings.Join(b.lines, "\n") + "\n```\n")
	case sbTable:
		w.b.WriteString("```\n" + slackGrid(b) + "\n```\n")
	case sbQuote:
		for _, l := range b.lines {
			w.b.WriteString("> " + slackInline(l, f) + "\n")
		}
	case sbItem:
		level := min(b.item.indent/2, len(mdBullets)-1)
		mark := mdBullets[level]
		if b.item.ordered {
			mark = b.item.num + "."
		}
		if t := itemCheck(b.item); t != "" {
			mark = strings.TrimSpace(t)
		}
		w.b.WriteString(strings.Repeat("    ", level) + mark + " " + slackInline(b.text, f) + "\n")
	}
}

// ---- tables ----------------------------------------------------------------

type plainFmt struct{}

func (plainFmt) text(s string) string                    { return s }
func (plainFmt) code(s string) string                    { return s }
func (plainFmt) emph(_ int, inner string, _ bool) string { return inner }
func (plainFmt) strike(inner string) string              { return inner }
func (plainFmt) link(text, _ string) string              { return text }

// slackGrid lays a table out as aligned monospaced text. Widths are display
// columns, so a cell in Japanese takes the room it is drawn in.
func slackGrid(b sBlock) string {
	n := len(b.head)
	rows := make([][]string, 0, len(b.rows)+1)
	for _, r := range append([][]string{b.head}, b.rows...) {
		cells := make([]string, n)
		for c := range cells {
			if c < len(r) {
				cells[c] = slackInline(r[c], plainFmt{})
			}
		}
		rows = append(rows, cells)
	}
	w := make([]int, n)
	for _, r := range rows {
		for c, s := range r {
			w[c] = max(w[c], lipgloss.Width(s))
		}
	}

	line := func(r []string) string {
		var sb strings.Builder
		for c, s := range r {
			if c > 0 {
				sb.WriteString(" | ")
			}
			gap := w[c] - lipgloss.Width(s)
			switch b.align[c] {
			case mdAlignRight:
				sb.WriteString(strings.Repeat(" ", gap) + s)
			case mdAlignCenter:
				sb.WriteString(strings.Repeat(" ", gap/2) + s + strings.Repeat(" ", gap-gap/2))
			default:
				sb.WriteString(s + strings.Repeat(" ", gap))
			}
		}
		return strings.TrimRight(sb.String(), " ")
	}

	out := []string{line(rows[0])}
	rule := make([]string, n)
	for c := range rule {
		rule[c] = strings.Repeat("-", w[c])
	}
	out = append(out, strings.Join(rule, "-+-"))
	for _, r := range rows[1:] {
		out = append(out, line(r))
	}
	return strings.Join(out, "\n")
}
