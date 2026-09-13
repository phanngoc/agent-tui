package ui

import (
	"encoding/json"
	"sort"
	"strings"

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
