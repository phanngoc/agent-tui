package ui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/phanngoc/agent-tui/internal/highlight"
	"github.com/phanngoc/agent-tui/internal/theme"
)

// Markdown, rendered for a terminal instead of shown as source.
//
// Models answer in markdown and a pane has no renderer for it, so the
// transcript used to print the syntax itself: ## before every heading,
// |---|---| where a table belongs, fences around code, asterisks around
// emphasis. This turns each construct into the thing it stands for — a
// coloured heading, aligned columns, an indented highlighted block, real bold
// — because the reader is a person looking at a pane, not a markdown parser.
//
// The renderer is small and forgiving on purpose. It runs on every streaming
// delta, over text that is usually half-written — an unclosed fence, a table
// still missing its last row — so every construct degrades into plain styled
// text rather than failing.

// mdMinWidth is the narrowest column count worth wrapping into. Below it the
// pane is too narrow for the result to mean anything anyway.
const mdMinWidth = 8

// renderMarkdown turns src into styled lines of at most w display columns. An
// empty string in the result is a real blank line.
func renderMarkdown(st *theme.Styles, sc *highlight.Scheme, src string, w int) []string {
	d := &mdDoc{st: st, sc: sc, w: max(mdMinWidth, w)}
	d.run(mdSplit(src))
	for len(d.out) > 0 && d.out[len(d.out)-1] == "" {
		d.out = d.out[:len(d.out)-1]
	}
	return d.out
}

type mdDoc struct {
	st    *theme.Styles
	sc    *highlight.Scheme
	w     int
	quote bool // rendering the inside of a blockquote
	out   []string
}

func mdSplit(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

func (d *mdDoc) push(l string) { d.out = append(d.out, l) }

// blank appends at most one empty line. Markdown's paragraph spacing is one
// line however many the author left, and a stray run of them in a reply is
// wasted height in a pane that is already short.
func (d *mdDoc) blank() {
	if len(d.out) == 0 || d.out[len(d.out)-1] == "" {
		return
	}
	d.push("")
}

// base is the colour for unmarked text in the current context.
func (d *mdDoc) base() lipgloss.Style {
	if d.quote {
		return d.st.MdQuote
	}
	return d.st.Body
}

// wrap appends s wrapped to the document width, prefixing the first line with
// head and every continuation with cont. cont should be as wide as head so the
// text stays in one column.
func (d *mdDoc) wrap(head, cont, s string) {
	if strings.TrimSpace(stripANSI(s)) == "" {
		return
	}
	avail := d.w - lipgloss.Width(head)
	if avail < mdMinWidth {
		avail = mdMinWidth
	}
	for i, l := range strings.Split(lipgloss.Wrap(s, avail, " "), "\n") {
		if i == 0 {
			d.push(head + l)
			continue
		}
		d.push(cont + l)
	}
}

func (d *mdDoc) run(lines []string) {
	for i := 0; i < len(lines); {
		l := lines[i]
		switch {
		case strings.TrimSpace(l) == "":
			d.blank()
			i++
		case mdFenceLen(l) > 0:
			i = d.code(lines, i)
		case mdHeadLevel(l) > 0:
			i = d.heading(lines, i)
		case mdIsRule(l):
			d.blank()
			d.push(d.st.MdRule.Render(strings.Repeat("─", d.w)))
			d.blank()
			i++
		case mdTableAt(lines, i):
			i = d.table(lines, i)
		case mdIsQuote(l):
			i = d.blockquote(lines, i)
		case mdIsItem(l):
			i = d.list(lines, i)
		default:
			i = d.para(lines, i)
		}
	}
}

// --- headings ---------------------------------------------------------------

func (d *mdDoc) heading(lines []string, i int) int {
	lv := mdHeadLevel(lines[i])
	_, t := mdIndent(lines[i])
	text := strings.TrimSpace(t[lv:])
	text = strings.TrimSpace(strings.TrimRight(text, "#"))
	d.emitHeading(lv, text)
	return i + 1
}

func (d *mdDoc) emitHeading(lv int, text string) {
	d.blank()
	if lv > 2 {
		d.wrap("", "", d.inline(text, d.st.MdSub))
		d.blank()
		return
	}
	// Upper case gives the top two levels the weight a rendered heading gets
	// from a larger font. A heading that names code is left alone: an
	// identifier is not the same word in another case.
	if !strings.ContainsRune(text, '`') {
		text = strings.ToUpper(text)
	}
	d.wrap(d.st.MdRail.Render("▌"), " ", d.inline(text, d.st.MdHead))
	d.blank()
}

// --- fenced code ------------------------------------------------------------

func (d *mdDoc) code(lines []string, i int) int {
	ind, t := mdIndent(lines[i])
	ch, n := t[0], mdFenceLen(lines[i])
	info := strings.TrimSpace(t[n:])
	if k := strings.IndexAny(info, " \t"); k >= 0 {
		info = info[:k]
	}

	body := make([]string, 0, 8)
	j := i + 1
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
	if len(body) == 0 {
		return j
	}

	d.blank()
	src := strings.Join(body, "\n")
	for _, l := range highlight.Render(d.sc, highlight.Detect(mdLangFile(info)), []byte(src)) {
		d.push("  " + clipLine(l, d.w-2))
	}
	d.blank()
	return j
}

// mdLangFile turns a fence's info string into a file name the highlighter can
// detect a language from. Unknown labels fall through to plain text.
func mdLangFile(info string) string {
	if info == "" {
		return "x.txt"
	}
	info = strings.ToLower(info)
	if f, ok := mdLangs[info]; ok {
		return f
	}
	return "x." + info
}

var mdLangs = map[string]string{
	"golang": "x.go", "rs": "x.rs", "rust": "x.rs",
	"c++": "x.cpp", "cplusplus": "x.cpp", "objective-c": "x.m",
	"javascript": "x.js", "typescript": "x.ts", "node": "x.js",
	"python": "x.py", "python3": "x.py", "ruby": "x.rb",
	"kotlin": "x.kt", "shell": "x.sh", "bash": "x.sh", "zsh": "x.zsh",
	"console": "x.sh", "sh": "x.sh", "terminal": "x.sh",
	"yml": "x.yaml", "terraform": "x.tf",
	"dockerfile": "Dockerfile", "docker": "Dockerfile",
	"make": "Makefile", "makefile": "Makefile",
	"markdown": "x.md", "text": "x.txt", "plain": "x.txt", "plaintext": "x.txt",
	"diff": "x.txt", "patch": "x.txt",
}

// --- tables -----------------------------------------------------------------

const (
	mdAlignLeft = iota
	mdAlignRight
	mdAlignCenter
)

func (d *mdDoc) table(lines []string, i int) int {
	head := mdCells(lines[i])
	n := len(head)
	align := mdAligns(lines[i+1], n)

	rows := [][]string{d.styleRow(head, n, d.st.MdTableHead)}
	j := i + 2
	for ; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) == "" || !strings.Contains(lines[j], "|") {
			break
		}
		rows = append(rows, d.styleRow(mdCells(lines[j]), n, d.base()))
	}

	w := mdWidths(rows, n, d.w)
	used := 2 * (n - 1)
	for _, x := range w {
		used += x
	}
	used = min(used, d.w)

	d.blank()
	d.push(d.row(rows[0], w, align))
	d.push(d.st.MdRule.Render(strings.Repeat("─", used)))
	for _, r := range rows[1:] {
		d.push(d.row(r, w, align))
	}
	d.blank()
	return j
}

func (d *mdDoc) row(cells []string, w, align []int) string {
	var b strings.Builder
	for c := range w {
		if c > 0 {
			b.WriteString("  ")
		}
		b.WriteString(d.pad(cells[c], w[c], align[c]))
	}
	return strings.TrimRight(b.String(), " ")
}

// pad fits one cell into its column, padding on the side the alignment asks
// for and eliding what does not fit.
func (d *mdDoc) pad(s string, w, align int) string {
	if lipgloss.Width(s) > w {
		if w <= 1 {
			return d.st.Faint.Render("…")
		}
		return clipLine(s, w-1) + d.st.Faint.Render("…")
	}
	gap := w - lipgloss.Width(s)
	switch align {
	case mdAlignRight:
		return strings.Repeat(" ", gap) + s
	case mdAlignCenter:
		left := gap / 2
		return strings.Repeat(" ", left) + s + strings.Repeat(" ", gap-left)
	default:
		return s + strings.Repeat(" ", gap)
	}
}

// mdWidths sizes the columns to their content, then shrinks the widest one
// repeatedly until the row fits the pane.
func mdWidths(rows [][]string, n, total int) []int {
	w := make([]int, n)
	for _, r := range rows {
		for c := 0; c < n; c++ {
			if x := lipgloss.Width(r[c]); x > w[c] {
				w[c] = x
			}
		}
	}
	for c := range w {
		w[c] = min(max(w[c], 1), total)
	}
	gutters := 2 * (n - 1)
	for {
		sum := gutters
		for _, x := range w {
			sum += x
		}
		if sum <= total {
			return w
		}
		widest, at := 0, -1
		for c, x := range w {
			if x > widest && x > 3 {
				widest, at = x, c
			}
		}
		if at < 0 {
			return w
		}
		w[at]--
	}
}

func (d *mdDoc) styleRow(cells []string, n int, base lipgloss.Style) []string {
	out := make([]string, n)
	for c := range out {
		if c < len(cells) {
			out[c] = d.inline(cells[c], base)
		}
	}
	return out
}

func mdCells(l string) []string {
	t := strings.TrimSpace(l)
	t = strings.TrimSuffix(strings.TrimPrefix(t, "|"), "|")
	cells := strings.Split(t, "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells
}

func mdAligns(divider string, n int) []int {
	out := make([]int, n)
	for i, c := range mdCells(divider) {
		if i >= n {
			break
		}
		left, right := strings.HasPrefix(c, ":"), strings.HasSuffix(c, ":")
		switch {
		case left && right:
			out[i] = mdAlignCenter
		case right:
			out[i] = mdAlignRight
		}
	}
	return out
}

// mdIsDivider reports whether l is a table's |---|:--:| rule.
func mdIsDivider(l string) bool {
	if !strings.Contains(l, "-") || !strings.Contains(l, "|") {
		return false
	}
	for _, c := range mdCells(l) {
		c = strings.TrimSuffix(strings.TrimPrefix(c, ":"), ":")
		if c == "" || strings.Trim(c, "-") != "" {
			return false
		}
	}
	return true
}

func mdTableAt(lines []string, i int) bool {
	return i+1 < len(lines) && strings.Contains(lines[i], "|") &&
		len(mdCells(lines[i])) > 1 && !mdIsDivider(lines[i]) &&
		mdIsDivider(lines[i+1])
}

// --- quotes, lists, paragraphs ----------------------------------------------

func (d *mdDoc) blockquote(lines []string, i int) int {
	body := make([]string, 0, 4)
	j := i
	for ; j < len(lines); j++ {
		if !mdIsQuote(lines[j]) {
			break
		}
		_, t := mdIndent(lines[j])
		body = append(body, strings.TrimPrefix(strings.TrimPrefix(t, ">"), " "))
	}

	// Quoted text is markdown too, so it goes through a nested document two
	// columns narrower and is then railed, rather than re-implemented here.
	sub := &mdDoc{st: d.st, sc: d.sc, w: max(mdMinWidth, d.w-2), quote: true}
	sub.run(body)

	d.blank()
	bar := d.st.MdQuoteBar.Render("│")
	for _, l := range sub.out {
		if l == "" {
			d.push(bar)
			continue
		}
		d.push(bar + " " + l)
	}
	d.blank()
	return j
}

var mdBullets = [...]string{"•", "◦", "▪", "·"}

func (d *mdDoc) list(lines []string, i int) int {
	for i < len(lines) {
		it, ok := mdItemAt(lines[i])
		if !ok {
			return i
		}
		i++
		// A wrapped item continues on the following more-indented lines; a
		// fence there opens a block of its own and ends the item.
		for i < len(lines) {
			ind, t := mdIndent(lines[i])
			if strings.TrimSpace(t) == "" || ind <= it.indent ||
				mdIsItem(lines[i]) || mdFenceLen(lines[i]) > 0 {
				break
			}
			it.text += " " + strings.TrimSpace(t)
			i++
		}
		d.item(it)
	}
	return i
}

func (d *mdDoc) item(it mdItem) {
	level := min(it.indent/2, len(mdBullets)-1)
	mark := d.st.MdMark.Render(mdBullets[level])
	switch {
	case it.task == mdTaskOpen:
		mark = d.st.MdMark.Render("☐")
	case it.task == mdTaskDone:
		mark = d.st.Good.Render("☑")
	case it.ordered:
		mark = d.st.MdMark.Render(it.num + ".")
	}
	head := strings.Repeat("  ", level) + mark + " "
	d.wrap(head, strings.Repeat(" ", lipgloss.Width(head)), d.inline(it.text, d.base()))
}

func (d *mdDoc) para(lines []string, i int) int {
	parts := make([]string, 0, 4)
	j := i
	for ; j < len(lines); j++ {
		l := lines[j]
		if strings.TrimSpace(l) == "" {
			break
		}
		if j > i {
			// A line of = or - directly under a single line underlines it into
			// a heading; anywhere else it is a rule and ends the paragraph.
			if lv := mdSetext(l); lv > 0 && j == i+1 {
				d.emitHeading(lv, strings.TrimSpace(lines[i]))
				return j + 1
			}
			if mdHeadLevel(l) > 0 || mdFenceLen(l) > 0 || mdIsRule(l) ||
				mdIsQuote(l) || mdIsItem(l) || mdTableAt(lines, j) {
				break
			}
		}
		parts = append(parts, strings.TrimSpace(l))
	}
	d.wrap("", "", d.inline(strings.Join(parts, " "), d.base()))
	return j
}

// --- inline -----------------------------------------------------------------

// inline renders markdown's inline syntax, using base for everything that is
// not marked up. Every run is styled explicitly: an unstyled one falls back to
// the terminal's default foreground, which on a dark theme is barely there.
func (d *mdDoc) inline(s string, base lipgloss.Style) string {
	var out, plain strings.Builder
	out.Grow(len(s) + 32)
	flush := func() {
		if plain.Len() > 0 {
			out.WriteString(base.Render(plain.String()))
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
				out.WriteString(d.st.MdCode.Render(strings.TrimSpace(s[i+n : end])))
				i = end + n
				continue
			}
		case '*', '_':
			if n, end, ok := mdEmph(s, i); ok {
				flush()
				out.WriteString(d.inline(s[i+n:end], mdEmphStyle(d.st, n)))
				i = end + n
				continue
			}
		case '~':
			if mdRun(s, i, '~') >= 2 {
				if end := mdFind(s, i+2, '~', 2); end > 0 {
					flush()
					out.WriteString(d.inline(s[i+2:end], d.st.MdStrike))
					i = end + 2
					continue
				}
			}
		case '!', '[':
			if text, url, n, ok := mdLink(s, i); ok {
				flush()
				out.WriteString(d.inline(text, d.st.MdLink))
				// The target is kept: a terminal has nothing to click, so a
				// link whose URL is hidden is a link that was thrown away.
				if url != "" && url != text {
					out.WriteString(d.st.Faint.Render(" (" + mdShortURL(url) + ")"))
				}
				i += n
				continue
			}
		case '<':
			if end := strings.IndexByte(s[i:], '>'); end > 1 && mdIsURL(s[i+1:i+end]) {
				flush()
				out.WriteString(d.st.MdLink.Render(s[i+1 : i+end]))
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

func mdEmphStyle(st *theme.Styles, n int) lipgloss.Style {
	switch n {
	case 1:
		return st.MdItalic
	case 2:
		return st.MdBold
	default:
		return st.MdBoldItalic
	}
}

// mdEmph matches an emphasis run opening at i, returning the marker length and
// the index of its closer.
func mdEmph(s string, i int) (int, int, bool) {
	c := s[i]
	n := min(mdRun(s, i, c), 3)
	if i+n >= len(s) || mdSpace(s[i+n]) {
		return 0, 0, false
	}
	// snake_case and __dunder__ are identifiers, not emphasis, so an
	// underscore only opens one at a word boundary.
	if c == '_' && i > 0 && mdWord(s[i-1]) {
		return 0, 0, false
	}
	end := mdFind(s, i+n, c, n)
	if end <= i+n || mdSpace(s[end-1]) {
		return 0, 0, false
	}
	if c == '_' && end+n < len(s) && mdWord(s[end+n]) {
		return 0, 0, false
	}
	return n, end, true
}

// mdLink matches [text](url) or ![alt](url) at i.
func mdLink(s string, i int) (string, string, int, bool) {
	j := i
	if s[j] == '!' {
		j++
	}
	if j >= len(s) || s[j] != '[' {
		return "", "", 0, false
	}
	shut := mdMatch(s, j, '[', ']')
	if shut < 0 || shut+1 >= len(s) || s[shut+1] != '(' {
		return "", "", 0, false
	}
	end := mdMatch(s, shut+1, '(', ')')
	if end < 0 {
		return "", "", 0, false
	}
	url := strings.TrimSpace(s[shut+2 : end])
	if k := strings.IndexAny(url, " \t"); k >= 0 {
		url = url[:k] // a title trailing the URL
	}
	text := s[j+1 : shut]
	if strings.TrimSpace(text) == "" {
		text = url
	}
	return text, url, end + 1 - i, true
}

// mdMatch finds the closer that balances the opener at i.
func mdMatch(s string, i int, open, shut byte) int {
	depth := 0
	for j := i; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case open:
			depth++
		case shut:
			if depth--; depth == 0 {
				return j
			}
		}
	}
	return -1
}

// mdFind returns the index of the next run of at least n c's at or after from.
func mdFind(s string, from int, c byte, n int) int {
	for i := from; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] != c {
			continue
		}
		run := mdRun(s, i, c)
		if run >= n {
			return i
		}
		i += run - 1
	}
	return -1
}

func mdRun(s string, i int, c byte) int {
	n := 0
	for i+n < len(s) && s[i+n] == c {
		n++
	}
	return n
}

func mdShortURL(u string) string {
	const limit = 48
	if len(u) <= limit {
		return u
	}
	return u[:limit-1] + "…"
}

func mdIsURL(s string) bool {
	return strings.Contains(s, "://") || strings.HasPrefix(s, "mailto:")
}

func mdPunct(c byte) bool { return strings.IndexByte("\\`*_{}[]()#+-.!|~><", c) >= 0 }
func mdSpace(c byte) bool { return c == ' ' || c == '\t' }
func mdWord(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c|0x20 >= 'a' && c|0x20 <= 'z')
}

// --- line classification ----------------------------------------------------

// mdIndent returns the leading whitespace in columns and the rest of the line.
func mdIndent(l string) (int, string) {
	n := 0
	for i := 0; i < len(l); i++ {
		switch l[i] {
		case ' ':
			n++
		case '\t':
			n += 4
		default:
			return n, l[i:]
		}
	}
	return n, ""
}

// mdUnindent drops up to n columns of leading whitespace, which is how a fence
// indented under a list item gets its code back to column zero.
func mdUnindent(l string, n int) string {
	for n > 0 && l != "" && (l[0] == ' ' || l[0] == '\t') {
		if l[0] == '\t' {
			n -= 4
		} else {
			n--
		}
		l = l[1:]
	}
	return l
}

func mdHeadLevel(l string) int {
	ind, t := mdIndent(l)
	if ind > 3 {
		return 0
	}
	n := mdRun(t, 0, '#')
	if n == 0 || n > 6 || n >= len(t) || !mdSpace(t[n]) {
		return 0
	}
	return n
}

// mdSetext reports the level of the heading a === or --- underline makes.
func mdSetext(l string) int {
	ind, t := mdIndent(l)
	t = strings.TrimSpace(t)
	if ind > 3 || t == "" {
		return 0
	}
	switch {
	case strings.Trim(t, "=") == "":
		return 1
	case len(t) > 1 && strings.Trim(t, "-") == "":
		return 2
	}
	return 0
}

func mdIsRule(l string) bool {
	ind, t := mdIndent(l)
	t = strings.ReplaceAll(strings.TrimSpace(t), " ", "")
	if ind > 3 || len(t) < 3 {
		return false
	}
	if t[0] != '-' && t[0] != '*' && t[0] != '_' {
		return false
	}
	return strings.Trim(t, string(t[0])) == ""
}

func mdIsQuote(l string) bool {
	ind, t := mdIndent(l)
	return ind <= 3 && strings.HasPrefix(t, ">")
}

// mdFenceLen returns the length of the fence opening l, or 0. The indent limit
// is loose enough to catch a fence nested under a list item.
func mdFenceLen(l string) int {
	ind, t := mdIndent(l)
	if ind > 8 || t == "" || (t[0] != '`' && t[0] != '~') {
		return 0
	}
	if n := mdRun(t, 0, t[0]); n >= 3 {
		return n
	}
	return 0
}

// mdIsCloser reports whether l closes a fence of n ch's and nothing else.
func mdIsCloser(l string, ch byte, n int) bool {
	_, t := mdIndent(l)
	if t == "" || t[0] != ch {
		return false
	}
	run := mdRun(t, 0, ch)
	return run >= n && strings.TrimSpace(t[run:]) == ""
}

const (
	mdTaskNone = iota
	mdTaskOpen
	mdTaskDone
)

type mdItem struct {
	indent  int
	ordered bool
	num     string
	task    int
	text    string
}

func mdIsItem(l string) bool {
	_, ok := mdItemAt(l)
	return ok
}

func mdItemAt(l string) (mdItem, bool) {
	ind, t := mdIndent(l)
	if len(t) < 2 || mdIsRule(l) {
		return mdItem{}, false
	}
	it := mdItem{indent: ind}
	switch t[0] {
	case '-', '*', '+':
		if !mdSpace(t[1]) {
			return mdItem{}, false
		}
		it.text = strings.TrimSpace(t[2:])
	default:
		n := 0
		for n < len(t) && t[n] >= '0' && t[n] <= '9' {
			n++
		}
		if n == 0 || n > 9 || n+1 >= len(t) || (t[n] != '.' && t[n] != ')') || !mdSpace(t[n+1]) {
			return mdItem{}, false
		}
		it.ordered, it.num, it.text = true, t[:n], strings.TrimSpace(t[n+2:])
	}
	switch {
	case strings.HasPrefix(it.text, "[ ] "):
		it.task, it.text = mdTaskOpen, it.text[4:]
	case strings.HasPrefix(it.text, "[x] "), strings.HasPrefix(it.text, "[X] "):
		it.task, it.text = mdTaskDone, it.text[4:]
	}
	return it, true
}
