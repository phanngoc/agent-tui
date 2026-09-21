package ui

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/phanngoc/agent-tui/internal/search"
)

func stripANSI(s string) string { return ansi.Strip(s) }

// clipLine truncates a styled line to w display cells without breaking escapes.
func clipLine(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "")
}

// clipBlock trims a block to at most h lines of at most w cells each.
func clipBlock(s string, w, h int) string {
	if h <= 0 || w <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for i, l := range lines {
		if lipgloss.Width(l) > w {
			lines[i] = clipLine(l, w)
		}
	}
	return strings.Join(lines, "\n")
}

func padRight(s string, w int) string {
	if n := w - lipgloss.Width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return clipLine(s, w)
}

func searchEmpty() search.Result { return search.Result{} }

// firstNonBlank returns the first value that is not empty.
func firstNonBlank(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// prettyInput renders a tool's JSON arguments as a few readable lines for the
// approval prompt. Long values are elided: the user is approving an action, not
// reviewing a diff.
func prettyInput(raw json.RawMessage, w int) []string {
	var m map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil {
		return []string{truncate(string(raw), w)}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]string, 0, len(keys)+2)
	for _, k := range keys {
		v, _ := m[k].(string)
		if v == "" {
			b, _ := json.Marshal(m[k])
			v = string(b)
		}
		lines := strings.Split(v, "\n")
		switch {
		case len(lines) == 1:
			out = append(out, k+": "+truncate(lines[0], max(8, w-len(k)-2)))
		default:
			out = append(out, k+": "+truncate(lines[0], max(8, w-len(k)-2)))
			for i := 1; i < len(lines) && i < 6; i++ {
				out = append(out, "  "+truncate(lines[i], max(8, w-4)))
			}
			if len(lines) > 6 {
				out = append(out, "  … "+plural(len(lines)-6, "more line"))
			}
		}
	}
	return out
}

// liveOutputBytes bounds what is kept of a running command's output. Only the
// tail is shown, and the tail is where a build says what went wrong.
const liveOutputBytes = 8 << 10

// tailOf keeps the last max bytes of s, cut at a line boundary so the first
// line shown is a whole one.
func tailOf(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[len(s)-max:]
	if i := strings.IndexByte(s, '\n'); i >= 0 && i < len(s)-1 {
		return s[i+1:]
	}
	return s
}

// lastLines returns at most n trailing non-empty-trimmed lines of s.
func lastLines(s string, n int) []string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// liveOutputRows is how much of a running command is on screen. Enough to see
// a test failing as it happens; not so much that a chatty build pushes the
// conversation off the top of the pane.
const liveOutputRows = 8

// truncateLeft cuts a string from its start, keeping the end.
//
// A path is known by its tail. Half the directories in a repository end up as
// ".../internal/ui" once the front is gone, and what tells them apart is what
// comes after — so when a path will not fit, the beginning is what goes.
func truncateLeft(s string, w int) string {
	if w <= 1 {
		return ""
	}
	n := lipgloss.Width(s)
	if n <= w {
		return s
	}
	return ansi.TruncateLeft(s, n-(w-1), "…")
}

// A note on characters whose width the terminal decides for itself.
//
// Unicode calls a set of them East Asian Ambiguous — one column beside Latin
// text, two beside CJK — and which a terminal draws is a matter of its font
// and its settings, not of anything measurable from here. Go counts them as
// one. Clipping cannot save a row that holds one: clipLine measures the same
// way, so the row passes every check here and is still drawn a column too wide,
// wraps, and lands its remainder at column zero inside the pane to the left.
//
// Most of them are drawn narrow in practice, which is why ▎ ✓ … · are used
// throughout. U+2212 MINUS SIGN is the one that is not: it is a maths symbol,
// and fonts give it the width of one. A diff count written with it put "−10"
// into the commit list beside it. Use ASCII "-" in anything laid out in
// columns; prose is free to use whatever it likes, because prose is wrapped to
// fit rather than placed.

// running renders how long something has been going, for a clock that is still
// moving.
//
// Coarser than shortDur, which reports what a finished call took and is worth
// a millisecond. A clock ticking in front of someone waiting is not: what it
// has to answer is "is this stuck", and "7m 52s" answers it where "472.318s"
// makes you do the arithmetic.
func running(since time.Time) string {
	if since.IsZero() {
		return ""
	}
	d := time.Since(since)
	if d < 0 {
		return ""
	}
	switch s := int(d.Seconds()); {
	case s < 60:
		return strconv.Itoa(s) + "s"
	case s < 3600:
		return strconv.Itoa(s/60) + "m " + strconv.Itoa(s%60) + "s"
	default:
		return strconv.Itoa(s/3600) + "h " + strconv.Itoa(s/60%60) + "m"
	}
}
