package highlight

import (
	"fmt"
	"image/color"
	"strings"
	"unicode/utf8"
)

// Kind enumerates the token classes the renderer can colour.
type Kind uint8

const (
	Plain Kind = iota
	Keyword
	Type
	String
	Number
	Comment
	Func
	Punct
	nKinds
)

const reset = "\x1b[0m"

// Scheme maps token classes to pre-built ANSI escape sequences. Building them
// once means the hot loop only concatenates strings.
type Scheme struct {
	esc [nKinds]string
}

// NewScheme converts colours into escape sequences. Truecolor is emitted
// unconditionally; Bubble Tea's colour profile writer downsamples it to what
// the terminal actually supports.
//
// plain is the colour for everything the lexer does not classify — identifiers,
// mostly, which is the bulk of any source file. Leaving it unset would hand
// those to the terminal's default foreground, which is how the preview ended up
// almost invisible against a dark background.
func NewScheme(plain, kw, ty, str, num, com, fn, punct color.Color) *Scheme {
	s := &Scheme{}
	set := func(k Kind, c color.Color) {
		if c == nil {
			return
		}
		r, g, b, _ := c.RGBA()
		s.esc[k] = fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r>>8, g>>8, b>>8)
	}
	set(Plain, plain)
	set(Keyword, kw)
	set(Type, ty)
	set(String, str)
	set(Number, num)
	set(Comment, com)
	set(Func, fn)
	set(Punct, punct)
	return s
}

// TabWidth controls how a tab is expanded in the preview. Expanding at render
// time keeps the viewport's column arithmetic honest.
const TabWidth = 4

// renderer accumulates every line into one byte buffer and records where each
// line ends. The result is materialised as a single string that all the line
// slices share, which keeps allocation at O(1) instead of O(lines).
type renderer struct {
	sc   *Scheme
	buf  []byte
	ends []int
	col  int
}

func (r *renderer) newline() {
	r.ends = append(r.ends, len(r.buf))
	r.col = 0
}

// lines materialises the accumulated buffer as one string per source line.
func (r *renderer) result() []string {
	r.ends = append(r.ends, len(r.buf))
	// The buffer is finished and never written again, so the line slices can
	// share it directly instead of paying for a full copy.
	all := bytesToString(r.buf)
	out := make([]string, len(r.ends))
	prev := 0
	for i, end := range r.ends {
		out[i] = all[prev:end]
		prev = end
	}
	return out
}

// write appends s in the given colour, expanding tabs and dropping CRs.
func (r *renderer) write(k Kind, s string) {
	if s == "" {
		return
	}
	esc := r.sc.esc[k]
	if esc != "" {
		r.buf = append(r.buf, esc...)
	}
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\t':
			r.buf = append(r.buf, s[start:i]...)
			r.col += countRunes(s[start:i])
			pad := TabWidth - r.col%TabWidth
			for p := 0; p < pad; p++ {
				r.buf = append(r.buf, ' ')
			}
			r.col += pad
			start = i + 1
		case '\r':
			r.buf = append(r.buf, s[start:i]...)
			r.col += countRunes(s[start:i])
			start = i + 1
		}
	}
	r.buf = append(r.buf, s[start:]...)
	r.col += countRunes(s[start:])
	if esc != "" {
		r.buf = append(r.buf, reset...)
	}
}

// emit writes s, splitting it across lines. Multi-line tokens (block comments,
// raw strings) go through here so each output line stays self-contained.
func (r *renderer) emit(k Kind, s string) {
	for {
		nl := strings.IndexByte(s, '\n')
		if nl < 0 {
			r.write(k, s)
			return
		}
		r.write(k, s[:nl])
		r.newline()
		s = s[nl+1:]
	}
}

func countRunes(s string) int {
	if isASCII(s) {
		return len(s)
	}
	return utf8.RuneCountInString(s)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// Render tokenises src and returns one ANSI-styled string per source line.
func Render(sc *Scheme, l *Lang, src []byte) []string {
	if l == nil {
		l = langPlain
	}
	nl := countNL(src)
	r := &renderer{
		sc:   sc,
		buf:  make([]byte, 0, len(src)+len(src)/2),
		ends: make([]int, 0, nl+1),
	}
	switch l {
	case langMarkdown:
		lexMarkdown(r, src)
	case langPlain:
		r.emit(Plain, bytesToString(src))
	default:
		lexCode(r, l, src)
	}
	lines := r.result()
	// A trailing newline in the source produces one empty line; drop the extra.
	if n := len(lines); n > 1 && lines[n-1] == "" && len(src) > 0 && src[len(src)-1] == '\n' {
		lines = lines[:n-1]
	}
	return lines
}

func countNL(b []byte) int {
	n := 0
	for _, c := range b {
		if c == '\n' {
			n++
		}
	}
	return n
}

// lexCode is the generic C-family-ish scanner. One pass, no backtracking.
func lexCode(r *renderer, l *Lang, src []byte) {
	kw, ty := l.sets()
	s := bytesToString(src)
	i, n := 0, len(s)

	for i < n {
		c := s[i]

		// Fast path for runs of plain whitespace and newlines.
		if c == '\n' {
			r.newline()
			i++
			continue
		}
		if c == ' ' || c == '\t' || c == '\r' {
			j := i
			for j < n && (s[j] == ' ' || s[j] == '\t' || s[j] == '\r') {
				j++
			}
			r.write(Plain, s[i:j])
			i = j
			continue
		}

		// Line comment.
		if lc := matchAny(s, i, l.LineComment); lc > 0 {
			j := strings.IndexByte(s[i:], '\n')
			if j < 0 {
				j = n
			} else {
				j += i
			}
			r.write(Comment, s[i:j])
			i = j
			continue
		}

		// Block comment, possibly spanning lines.
		if l.BlockOpen != "" && strings.HasPrefix(s[i:], l.BlockOpen) {
			end := strings.Index(s[i+len(l.BlockOpen):], l.BlockClose)
			if end < 0 {
				r.emit(Comment, s[i:])
				return
			}
			stop := i + len(l.BlockOpen) + end + len(l.BlockClose)
			r.emit(Comment, s[i:stop])
			i = stop
			continue
		}

		// Raw / multi-line string.
		if d := matchAny(s, i, l.Raw); d > 0 {
			delim := s[i : i+d]
			end := strings.Index(s[i+d:], delim)
			if end < 0 {
				r.emit(String, s[i:])
				return
			}
			stop := i + d + end + d
			r.emit(String, s[i:stop])
			i = stop
			continue
		}

		// Single-line string.
		if l.Quotes != "" && strings.IndexByte(l.Quotes, c) >= 0 {
			j := i + 1
			for j < n && s[j] != c && s[j] != '\n' {
				if s[j] == '\\' && !l.NoEscape && j+1 < n {
					j++
				}
				j++
			}
			if j < n && s[j] == c {
				j++
			}
			r.write(String, s[i:j])
			i = j
			continue
		}

		// Number.
		if isDigit(c) || (c == '.' && i+1 < n && isDigit(s[i+1]) && !identByte(prevByte(s, i))) {
			j := i
			for j < n && (isDigit(s[j]) || s[j] == '.' || s[j] == '_' ||
				s[j] == 'x' || s[j] == 'X' || s[j] == 'b' || s[j] == 'o' ||
				isHex(s[j]) || ((s[j] == '+' || s[j] == '-') && (s[j-1] == 'e' || s[j-1] == 'E'))) {
				j++
			}
			r.write(Number, s[i:j])
			i = j
			continue
		}

		// Identifier / keyword / type / call.
		if identStart(c) {
			j := i + 1
			for j < n && identByte(s[j]) {
				j++
			}
			word := s[i:j]
			kind := Plain
			if _, ok := kw[word]; ok {
				kind = Keyword
			} else if _, ok := ty[word]; ok {
				kind = Type
			} else if k := skipSpace(s, j); k < n && s[k] == '(' {
				kind = Func
			} else if isUpperStart(word) {
				kind = Type
			}
			r.write(kind, word)
			i = j
			continue
		}

		// Anything else is punctuation; batch the run.
		j := i
		for j < n && !identStart(s[j]) && !isDigit(s[j]) && s[j] != '\n' &&
			s[j] != ' ' && s[j] != '\t' && s[j] != '\r' &&
			(l.Quotes == "" || strings.IndexByte(l.Quotes, s[j]) < 0) &&
			matchAny(s, j, l.LineComment) == 0 && matchAny(s, j, l.Raw) == 0 &&
			(l.BlockOpen == "" || !strings.HasPrefix(s[j:], l.BlockOpen)) {
			j++
		}
		if j == i {
			j++ // always make progress
		}
		r.write(Punct, s[i:j])
		i = j
	}
}

// lexMarkdown gives headings, fences, emphasis and links a light treatment.
func lexMarkdown(r *renderer, src []byte) {
	s := bytesToString(src)
	inFence := false
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		switch {
		case strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~"):
			inFence = !inFence
			r.write(Comment, line)
		case inFence:
			r.write(String, line)
		case strings.HasPrefix(trimmed, "#"):
			r.write(Keyword, line)
		case strings.HasPrefix(trimmed, ">"):
			r.write(Comment, line)
		case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") ||
			strings.HasPrefix(trimmed, "+ ") || strings.HasPrefix(trimmed, "|"):
			r.write(Punct, line)
		default:
			r.write(Plain, line)
		}
		r.newline()
	}
	// Split produced a final empty element; result() adds the terminator itself.
	if n := len(r.ends); n > 0 {
		r.ends = r.ends[:n-1]
	}
}

func matchAny(s string, i int, pats []string) int {
	for _, p := range pats {
		if p != "" && strings.HasPrefix(s[i:], p) {
			return len(p)
		}
	}
	return 0
}

func skipSpace(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}

func prevByte(s string, i int) byte {
	if i == 0 {
		return 0
	}
	return s[i-1]
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isHex(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
func identStart(c byte) bool {
	return c == '_' || c == '$' || c == '@' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}
func identByte(c byte) bool { return identStart(c) || isDigit(c) }
func isUpperStart(w string) bool {
	return len(w) > 1 && w[0] >= 'A' && w[0] <= 'Z'
}

// ExpandTabs replaces tabs with the spaces a terminal draws in their place, and
// drops carriage returns.
//
// A tab is one character and some number of columns, and the two are not the
// same number. Everything that lays text out in columns — a diff pane, a
// transcript, a preview — has to agree with the terminal about how wide a line
// is, and a line measured at one column per tab and drawn at four overflows
// into whatever is beside it. Expanding once, early, is what keeps that
// arithmetic honest; it is the same thing the renderer above does inline.
func ExpandTabs(s string) string {
	if !strings.ContainsAny(s, "\t\r") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	col := 0
	for _, r := range s {
		switch r {
		case '\t':
			pad := TabWidth - col%TabWidth
			for i := 0; i < pad; i++ {
				b.WriteByte(' ')
			}
			col += pad
		case '\r':
			// A terminal would put the cursor back at the start of the line,
			// which in a pane of stacked rows is not a thing that can happen.
		default:
			b.WriteRune(r)
			col++
		}
	}
	return b.String()
}
