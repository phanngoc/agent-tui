package ui

import (
	"context"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// Ctrl+click a file in the transcript to read it.
//
// Agents end their turns with lists of what they made — "SKILL.md",
// "Correctness checklist" — drawn as links, and their prose is full of paths:
// ekyc.service.ts:256, skills/brainstorm-business-logic. Each one was a thing
// to retype into the finder. Now a ctrl+click on one opens it in the preview,
// at its line when it names one, from the filesystem the session works on —
// a WSL path from a WSL session, a container path from a container's.
//
// What is drawn is not enough to know where a link goes: the renderer shows
// the link's text and a shortened URL, and the line may cut even that. So the
// click is matched against the message it landed in, as written: the link
// whose drawn text the click is on, or whose URL follows it, is the one meant,
// and its target is the whole URL from the source. A click on plain text takes
// the path-like word under the pointer instead, and opens it only if it is a
// file — a ctrl+click on an ordinary word does nothing but say so.
//
// A plain click is still the start of a selection; ctrl is what makes it a
// request to open.

// linkTarget is where a click points, before it is resolved.
type linkTarget struct {
	path string
	line int
}

// openLinkAt opens what a ctrl+click in the transcript points at. ok is false
// when the click was not in the transcript at all.
func (m *Model) openLinkAt(x, y int) (tea.Cmd, bool) {
	left, top, w, h, ok := m.paneBox(focusChat)
	if !ok || x < left || x >= left+w || y < top || y >= top+h {
		return nil, false
	}
	row := m.chat.YOffset() + y - top
	col := x - left
	lines := strings.Split(m.chatSet, "\n")
	if row < 0 || row >= len(lines) {
		return nil, true
	}
	plain := ansi.Strip(lines[row])

	tgt, found := findLinkAt(plain, col, m.sourceAt(row))
	if !found {
		m.notice = "nothing to open there — ctrl+click a file link or a path"
		return nil, true
	}
	if isWebURL(tgt.path) {
		m.notice = "opening " + tgt.path
		return openInBrowser(tgt.path), true
	}
	return m.openTargetCmd(tgt), true
}

// sourceAt is the markdown of the message drawn at a transcript row: the
// committed message whose block contains it, or the answer still streaming
// below them.
func (m *Model) sourceAt(row int) string {
	s := m.mgr.Active()
	starts := m.chatStarts
	if len(starts) != len(s.Messages) {
		return s.Partial
	}
	at := -1
	for i, st := range starts {
		if st <= row {
			at = i
		}
	}
	headEnd := strings.Count(m.chatCache, "\n")
	if row > headEnd || at < 0 {
		return s.Partial
	}
	return messageSource(s.Messages[at])
}

// messageSource is everything in a message a link could have been drawn
// from: its text, and what its tool calls were given and returned.
func messageSource(msg session.Message) string {
	var b strings.Builder
	b.WriteString(msg.Text)
	for _, t := range msg.Tools {
		b.WriteByte('\n')
		b.Write(t.Input)
		b.WriteByte('\n')
		b.WriteString(t.Result)
	}
	return b.String()
}

var (
	mdLinkRe   = regexp.MustCompile(`!?\[([^\]]*)\]\(\s*<?([^)\s>]+)>?(?:\s+"[^"]*")?\s*\)`)
	autoLinkRe = regexp.MustCompile(`<((?:https?|file)://[^>\s]+)>`)
	lineRe     = regexp.MustCompile(`(?:#L(\d+)(?:-L?\d+)?|:(\d+)(?::\d+)?)$`)
	// pathish is a word that names a file: it has a separator, or a dot and an
	// extension of a few letters, optionally followed by :line.
	pathish = regexp.MustCompile(`^(?:[~.]?[\w\-.@+]*[/\\][\w\-.@+/\\]*|[\w\-@+][\w\-@+.]*\.[A-Za-z0-9]{1,8})(?::\d+(?::\d+)?)?$`)
	mdMarks = strings.NewReplacer("**", "", "__", "", "`", "", "~~", "")
)

// findLinkAt works out what a click at a display column of a drawn line
// points at, using the markdown the line was drawn from.
func findLinkAt(plain string, col int, src string) (linkTarget, bool) {
	h, ok := findLinkSpan(plain, col, src)
	return h.target, ok
}

// linkHit is a link found under a column: where it goes, and the columns of
// the line it covers, for underlining it.
type linkHit struct {
	target linkTarget
	lo, hi int
}

// findLinkSpan is findLinkAt with the span of what was found.
func findLinkSpan(plain string, col int, src string) (linkHit, bool) {
	// Links first: they are what the reader was offered as clickable.
	for _, mt := range mdLinkRe.FindAllStringSubmatch(src, -1) {
		text := strings.TrimSpace(mdMarks.Replace(mt[1]))
		url := mt[2]
		if text == "" {
			text = url
		}
		for _, at := range occurrences(plain, text) {
			lo := ansi.StringWidth(plain[:at])
			hi := lo + ansi.StringWidth(text)
			// The shortened URL drawn after the text belongs to the link too.
			if url != text {
				if rest := plain[at+len(text):]; strings.HasPrefix(rest, " (") {
					end := strings.IndexByte(rest, ')')
					if end < 0 {
						end = len(rest)
					}
					hi += ansi.StringWidth(rest[:min(end+1, len(rest))])
				}
			}
			if col >= lo && col < hi {
				return linkHit{splitLine(url), lo, hi}, true
			}
		}
	}
	var urls []string
	for _, mt := range mdLinkRe.FindAllStringSubmatch(src, -1) {
		urls = append(urls, mt[2])
	}
	for _, mt := range autoLinkRe.FindAllStringSubmatch(src, -1) {
		urls = append(urls, mt[1])
		for _, at := range occurrences(plain, mt[1]) {
			lo := ansi.StringWidth(plain[:at])
			if hi := lo + ansi.StringWidth(mt[1]); col >= lo && col < hi {
				return linkHit{splitLine(mt[1]), lo, hi}, true
			}
		}
	}

	// Then the word under the pointer.
	lo, hi, word := wordSpanAt(plain, col)
	if word == "" {
		return linkHit{}, false
	}
	// A piece of a link's URL — the part that wrapped onto the next line,
	// say — belongs to that link, and opens all of it.
	if len(word) >= 8 {
		for _, u := range urls {
			if strings.Contains(u, word) {
				return linkHit{splitLine(u), lo, hi}, true
			}
		}
	}
	if isWebURL(word) {
		return linkHit{linkTarget{path: word}, lo, hi}, true
	}
	if !pathish.MatchString(word) {
		return linkHit{}, false
	}
	return linkHit{splitLine(word), lo, hi}, true
}

// occurrences lists the byte offsets s is found at in line.
func occurrences(line, s string) []int {
	var out []int
	for from := 0; s != "" && from <= len(line); {
		i := strings.Index(line[from:], s)
		if i < 0 {
			break
		}
		out = append(out, from+i)
		from += i + len(s)
	}
	return out
}

// wordAtCol is the run of path characters around a display column, without
// the punctuation prose wraps around a path.
func wordAtCol(line string, col int) string {
	_, _, w := wordSpanAt(line, col)
	return w
}

// wordSpanAt is wordAtCol with the display columns the word covers.
func wordSpanAt(line string, col int) (lo, hi int, word string) {
	b0, b1 := wordBytesAt(line, col)
	if b0 >= b1 {
		return 0, 0, ""
	}
	word = strings.TrimRight(line[b0:b1], ".,:;!?…")
	lo = ansi.StringWidth(line[:b0])
	return lo, lo + ansi.StringWidth(word), word
}

// wordBytesAt is the byte range of the run of path characters around a
// display column; empty when the column is on a stop or past the line.
func wordBytesAt(line string, col int) (int, int) {
	isStop := func(r rune) bool {
		return r == ' ' || r == '\t' || strings.ContainsRune("()[]{}<>\"'`,;|│", r)
	}
	// Find the byte offset of the column.
	at, w := -1, 0
	for i, r := range line {
		rw := ansi.StringWidth(string(r))
		if col >= w && col < w+max(1, rw) {
			at = i
			break
		}
		w += rw
	}
	if at < 0 {
		return 0, 0
	}
	if r, _ := utf8.DecodeRuneInString(line[at:]); isStop(r) {
		return 0, 0
	}
	lo := at
	for lo > 0 {
		r, size := utf8.DecodeLastRuneInString(line[:lo])
		if isStop(r) {
			break
		}
		lo -= size
	}
	hi := at
	for hi < len(line) {
		r, size := utf8.DecodeRuneInString(line[hi:])
		if isStop(r) {
			break
		}
		hi += size
	}
	return lo, hi
}

// splitLine separates a line number from a path: file.go:42, file.go#L42.
func splitLine(p string) linkTarget {
	p = strings.TrimPrefix(p, "file://")
	if m := lineRe.FindStringSubmatchIndex(p); m != nil {
		n := ""
		switch {
		case m[2] >= 0:
			n = p[m[2]:m[3]]
		case m[4] >= 0:
			n = p[m[4]:m[5]]
		}
		// A Windows drive is not a line number: C:\x has no digits after the
		// colon, and this only ever takes digits, but "C:" alone must stay.
		if line, err := strconv.Atoi(n); err == nil && m[0] > 1 {
			return linkTarget{path: p[:m[0]], line: line}
		}
	}
	return linkTarget{path: p}
}

func isWebURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// linkResolvedMsg is a target checked against the session's filesystem.
type linkResolvedMsg struct {
	seq      int
	abs, rel string
	line     int
	err      string
}

// openTargetCmd resolves a target on the session's filesystem and opens it.
// Resolving can mean asking WSL or a container, so it happens off the update
// loop; the preview opens when the answer comes back.
func (m *Model) openTargetCmd(t linkTarget) tea.Cmd {
	s := m.mgr.Active()
	fsys := m.sessionFS(s)
	cwd := m.sessionCWD(s)
	m.prevSeq++
	seq := m.prevSeq
	p := t.path
	if strings.HasPrefix(p, "~/") || p == "~" {
		p = vfs.Join(fsys.DefaultDir(), strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
	}
	abs := p
	if !vfs.IsAbs(p) {
		abs = vfs.CleanPath(vfs.Join(cwd, p))
	}
	m.notice = "opening " + t.path
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		st, err := fsys.Stat(ctx, abs)
		switch {
		case err != nil:
			return linkResolvedMsg{seq: seq, err: "no file at " + abs}
		case st.Dir:
			return linkResolvedMsg{seq: seq, err: abs + " is a folder — /cd to work there"}
		}
		rel := vfs.Rel(cwd, abs)
		if rel == "" || strings.HasPrefix(rel, "..") {
			rel = abs
		}
		return linkResolvedMsg{seq: seq, abs: abs, rel: rel, line: t.line}
	}
}

// linkResolved opens a resolved target in the preview, making room for it if
// the preview was closed.
func (m *Model) linkResolved(msg linkResolvedMsg) tea.Cmd {
	if msg.seq != m.prevSeq {
		return nil // something else was opened since
	}
	if msg.err != "" {
		m.notice = msg.err
		return nil
	}
	if !m.showPreview || m.showBtw {
		m.togglePreview()
	}
	where := msg.rel
	if msg.line > 0 {
		where += ":" + strconv.Itoa(msg.line)
	}
	m.notice = "opened " + where
	return m.loadFileAt(msg.abs, msg.rel, msg.line, false)
}

// openInBrowser hands a web address to whatever this machine opens them with.
func openInBrowser(url string) tea.Cmd {
	return func() tea.Msg {
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "windows":
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
		case "darwin":
			cmd = exec.Command("open", url)
		default:
			cmd = exec.Command("xdg-open", url)
		}
		_ = cmd.Start()
		return nil
	}
}

// ---- hovering ----------------------------------------------------------------

// linkHover is the link under the pointer, as it was when the pointer got
// there. It belongs to one drawing of the transcript at one scroll position,
// and is dropped from view when either has moved on.
type linkHover struct {
	on       bool
	row      int // transcript line
	lo, hi   int // columns of it
	target   string
	ver, top int // the transcript version and scroll it was found at
	cell     [2]int
}

// hoverAt follows the pointer over the transcript, finding the link under it.
// It does the work only when the pointer reaches another cell.
func (m *Model) hoverAt(x, y int) {
	if m.hover.on && m.hover.cell == [2]int{x, y} && m.hover.ver == m.chatVer && m.hover.top == m.chat.YOffset() {
		return
	}
	m.hover = linkHover{cell: [2]int{x, y}}
	left, top, w, h, ok := m.paneBox(focusChat)
	if !ok || x < left || x >= left+w || y < top || y >= top+h {
		return
	}
	row := m.chat.YOffset() + y - top
	lines := strings.Split(m.chatSet, "\n")
	if row < 0 || row >= len(lines) {
		return
	}
	hit, found := findLinkSpan(ansi.Strip(lines[row]), x-left, m.sourceAt(row))
	if !found {
		return
	}
	target := hit.target.path
	if hit.target.line > 0 {
		target += ":" + strconv.Itoa(hit.target.line)
	}
	m.hover = linkHover{
		on: true, row: row, lo: hit.lo, hi: hit.hi, target: target,
		ver: m.chatVer, top: m.chat.YOffset(), cell: [2]int{x, y},
	}
}

// hoverShown reports whether the hovered link is still where it was found.
func (m *Model) hoverShown() bool {
	return m.hover.on && m.hover.ver == m.chatVer && m.hover.top == m.chat.YOffset()
}

// paintHover underlines the hovered link in the drawn transcript.
func (m *Model) paintHover(view string) string {
	if !m.hoverShown() {
		return view
	}
	r := m.hover.row - m.chat.YOffset()
	lines := strings.Split(view, "\n")
	if r < 0 || r >= len(lines) {
		return view
	}
	l := lines[r]
	lo, hi := m.hover.lo, m.hover.hi
	if hi <= lo || lo >= ansi.StringWidth(l) {
		return view
	}
	mid := ansi.Strip(ansi.Cut(l, lo, hi))
	lines[r] = ansi.Cut(l, 0, lo) + m.st.MdLink.Underline(true).Render(mid) + ansi.TruncateLeft(l, hi, "")
	return strings.Join(lines, "\n")
}

// hoverStatus is what the status line says while a link is hovered: where it
// goes, in full — a link in a table shows only its text, and a long URL may be
// cut by the pane — and how to follow it.
func (m *Model) hoverStatus() string {
	if !m.hoverShown() {
		return ""
	}
	return m.st.MdLink.Render("↗ "+truncateLeft(m.hover.target, max(20, m.w-40))) +
		m.st.Faint.Render("  ctrl+click opens")
}
