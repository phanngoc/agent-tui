package git

import (
	"strconv"
	"strings"
)

// Reading a diff is mostly a search for the part that actually changed. A
// rewritten line shows up whole in red and whole in green, and the eye has to
// compare two long strings to find the one word that moved — which is exactly
// the work a tool should have done. So a replaced line carries spans marking
// where it differs from the line it replaced, and the renderer paints only
// those brighter.

// LineKind is what a diff line does.
type LineKind int

const (
	Context LineKind = iota
	Added
	Deleted
	// Meta is a "\ No newline at end of file" note, which belongs to neither
	// side and must not be counted as a change.
	Meta
)

// Span marks a byte range within a line's text.
type Span struct{ Start, End int }

// Line is one line of a diff.
type Line struct {
	Kind LineKind
	// Old and New are the line numbers on each side, zero where the line does
	// not exist on that side.
	Old, New int
	Text     string
	// Spans are the parts that differ from the line this one replaces. Empty
	// when there is no counterpart, or when so much changed that highlighting
	// the difference would just paint the whole line.
	Spans []Span
}

// Hunk is one @@ block.
type Hunk struct {
	// Header is the text git puts after the second @@, usually the enclosing
	// function. It is the one piece of context a folded hunk can show.
	Header string
	Lines  []Line
}

// File is a parsed patch for one path.
type File struct {
	Path   string
	Old    string
	Hunks  []Hunk
	Binary bool
}

// Added and Deleted count the lines a file's hunks change.
func (f File) Added() int   { return f.count(Added) }
func (f File) Deleted() int { return f.count(Deleted) }

func (f File) count(k LineKind) int {
	var n int
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.Kind == k {
				n++
			}
		}
	}
	return n
}

// ParsePatch reads a unified diff, which may cover several files.
func ParsePatch(patch string) []File {
	var files []File
	var cur *File
	var hunk *Hunk
	oldNum, newNum := 0, 0

	flushHunk := func() {
		if hunk != nil && cur != nil {
			markSpans(hunk)
			cur.Hunks = append(cur.Hunks, *hunk)
		}
		hunk = nil
	}
	flushFile := func() {
		flushHunk()
		if cur != nil {
			files = append(files, *cur)
		}
		cur = nil
	}

	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimSuffix(line, "\r")
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flushFile()
			cur = &File{Path: pathFromDiffHeader(line)}

		case cur == nil:
			// Preamble before the first file header; nothing to attach it to.
			continue

		case strings.HasPrefix(line, "rename from "):
			cur.Old = strings.TrimPrefix(line, "rename from ")
		case strings.HasPrefix(line, "rename to "):
			cur.Path = strings.TrimPrefix(line, "rename to ")
		case strings.HasPrefix(line, "Binary files "),
			strings.HasPrefix(line, "GIT binary patch"):
			cur.Binary = true

		case strings.HasPrefix(line, "+++ b/"):
			// The authoritative new path, which survives odd names better than
			// the `diff --git` line does.
			cur.Path = strings.TrimPrefix(line, "+++ b/")
		case strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "index "),
			strings.HasPrefix(line, "new file mode"), strings.HasPrefix(line, "deleted file mode"),
			strings.HasPrefix(line, "old mode"), strings.HasPrefix(line, "new mode"),
			strings.HasPrefix(line, "similarity index"), strings.HasPrefix(line, "dissimilarity index"):
			continue

		case strings.HasPrefix(line, "@@"):
			flushHunk()
			var ok bool
			oldNum, newNum, ok = parseHunkHeader(line)
			if !ok {
				continue
			}
			hunk = &Hunk{Header: hunkHeaderText(line)}

		case hunk == nil:
			continue

		case strings.HasPrefix(line, "+"):
			hunk.Lines = append(hunk.Lines, Line{
				Kind: Added, New: newNum, Text: line[1:],
			})
			newNum++
		case strings.HasPrefix(line, "-"):
			hunk.Lines = append(hunk.Lines, Line{
				Kind: Deleted, Old: oldNum, Text: line[1:],
			})
			oldNum++
		case strings.HasPrefix(line, `\`):
			hunk.Lines = append(hunk.Lines, Line{Kind: Meta, Text: strings.TrimSpace(line)})
		case strings.HasPrefix(line, " "):
			hunk.Lines = append(hunk.Lines, Line{
				Kind: Context, Old: oldNum, New: newNum, Text: line[1:],
			})
			oldNum++
			newNum++
		case line == "":
			// Git writes a bare empty line for an empty context line.
			hunk.Lines = append(hunk.Lines, Line{
				Kind: Context, Old: oldNum, New: newNum,
			})
			oldNum++
			newNum++
		}
	}
	flushFile()
	return files
}

// pathFromDiffHeader pulls the new path out of `diff --git a/x b/x`.
func pathFromDiffHeader(line string) string {
	rest := strings.TrimPrefix(line, "diff --git ")
	if i := strings.Index(rest, " b/"); i >= 0 {
		return rest[i+3:]
	}
	return rest
}

// parseHunkHeader reads the starting line numbers from `@@ -a,b +c,d @@`.
func parseHunkHeader(line string) (old, new int, ok bool) {
	rest, found := strings.CutPrefix(line, "@@ ")
	if !found {
		return 0, 0, false
	}
	end := strings.Index(rest, " @@")
	if end < 0 {
		return 0, 0, false
	}
	var gotOld, gotNew bool
	for _, part := range strings.Fields(rest[:end]) {
		if len(part) < 2 {
			continue
		}
		n, err := strconv.Atoi(strings.SplitN(part[1:], ",", 2)[0])
		if err != nil {
			continue
		}
		switch part[0] {
		case '-':
			old, gotOld = n, true
		case '+':
			new, gotNew = n, true
		}
	}
	return old, new, gotOld && gotNew
}

func hunkHeaderText(line string) string {
	if i := strings.Index(line, " @@"); i >= 0 && len(line) > i+3 {
		return strings.TrimSpace(line[i+3:])
	}
	return ""
}

// markSpans pairs each run of deletions with the additions that replaced it
// and records where they differ.
//
// Pairing is positional, which is what a line-oriented diff gives us: git has
// already decided these lines correspond, and going further — matching them by
// similarity — buys a better answer on reordered blocks at the cost of being
// wrong in a way that is hard to see. Runs of unequal length pair as far as
// they can and leave the rest whole.
func markSpans(h *Hunk) {
	for i := 0; i < len(h.Lines); {
		if h.Lines[i].Kind != Deleted {
			i++
			continue
		}
		delStart := i
		for i < len(h.Lines) && h.Lines[i].Kind == Deleted {
			i++
		}
		addStart := i
		for i < len(h.Lines) && h.Lines[i].Kind == Added {
			i++
		}
		dels := h.Lines[delStart:addStart]
		adds := h.Lines[addStart:i]
		for j := 0; j < len(dels) && j < len(adds); j++ {
			ds, as := diffSpans(dels[j].Text, adds[j].Text)
			dels[j].Spans, adds[j].Spans = ds, as
		}
	}
}

// diffSpans finds the middle of two lines that differ, by trimming the prefix
// and suffix they share.
//
// The trim is on rune boundaries, so a multi-byte character is never cut in
// half, and it stops at the start of a word: a shared "f" between "fpaas" and
// "feature" is a coincidence of spelling, not something worth leaving unlit.
//
// A change covering nearly the whole line gets no spans at all. Highlighting
// there would paint almost everything and say nothing, and the flat colour of
// a wholly rewritten line is already the right signal.
func diffSpans(a, b string) (spansA, spansB []Span) {
	if a == b || a == "" || b == "" {
		return nil, nil
	}
	pre := commonPrefix(a, b)
	suf := commonSuffix(a[pre:], b[pre:])

	pre = backToWordStart(a, pre)
	suf = forwardToWordEnd(a, len(a)-suf) // returns a byte count from the end
	suf = min(suf, min(len(a)-pre, len(b)-pre))

	midA, midB := len(a)-suf-pre, len(b)-suf-pre
	if midA <= 0 && midB <= 0 {
		return nil, nil
	}
	// Both sides must keep something recognisable for the highlight to mean
	// "this part changed" rather than "this line changed".
	const mostlyRewritten = 0.75
	if float64(midA) > float64(len(a))*mostlyRewritten &&
		float64(midB) > float64(len(b))*mostlyRewritten {
		return nil, nil
	}
	if midA > 0 {
		spansA = []Span{{Start: pre, End: pre + midA}}
	}
	if midB > 0 {
		spansB = []Span{{Start: pre, End: pre + midB}}
	}
	return spansA, spansB
}

func commonPrefix(a, b string) int {
	n := min(len(a), len(b))
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	// Do not split a multi-byte rune. i reaches len(a) when one line is a
	// prefix of the other, which is the common case for an appended word.
	for i > 0 && i < len(a) && isCont(a[i]) {
		i--
	}
	return i
}

func commonSuffix(a, b string) int {
	n := min(len(a), len(b))
	i := 0
	for i < n && a[len(a)-1-i] == b[len(b)-1-i] {
		i++
	}
	for i > 0 && isCont(a[len(a)-i]) {
		i--
	}
	return i
}

// isCont reports a UTF-8 continuation byte.
func isCont(c byte) bool { return c&0xC0 == 0x80 }

// backToWordStart walks a prefix length back to the start of the word it lands
// inside, so the highlight begins where the word does.
func backToWordStart(s string, i int) int {
	for i > 0 && isWordByte(s[i-1]) && i < len(s) && isWordByte(s[i]) {
		i--
	}
	for i > 0 && i < len(s) && isCont(s[i]) {
		i--
	}
	return i
}

// forwardToWordEnd does the same for a suffix, given a byte offset, and
// returns the suffix length again.
func forwardToWordEnd(s string, start int) int {
	i := start
	for i < len(s) && isWordByte(s[i]) && i > 0 && isWordByte(s[i-1]) {
		i++
	}
	for i < len(s) && isCont(s[i]) {
		i++
	}
	return len(s) - i
}

func isWordByte(c byte) bool {
	return c == '_' || c >= 0x80 ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
