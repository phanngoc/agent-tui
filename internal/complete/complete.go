// Package complete provides shell-style tab completion for the prompt.
//
// It behaves the way a terminal does: Tab extends the word under the cursor to
// the longest unambiguous prefix, and when several candidates remain it offers
// them as a menu to cycle through. Paths are read through the session's
// filesystem, so completing inside a container lists that container's files.
package complete

import (
	"context"
	"sort"
	"strings"

	"github.com/phanngoc/agent-tui/internal/vfs"
)

// Token is the word being completed, located in the line by rune index.
type Token struct {
	Text  string
	Start int
	End   int
	Quote byte // 0, '\'' or '"' when the word was opened with a quote
}

// Candidate is one possible completion.
type Candidate struct {
	// Insert replaces the token, already quoted if it needs to be.
	Insert string
	// Display is what the menu shows.
	Display string
	Dir     bool
}

// Result is everything the UI needs to apply a completion.
type Result struct {
	Token      Token
	Candidates []Candidate
	// Common is the longest prefix shared by every candidate. Inserting it is
	// what makes a single Tab feel like a shell.
	Common string
}

// Unambiguous reports whether there is exactly one way to finish the word.
func (r Result) Unambiguous() bool { return len(r.Candidates) == 1 }

// Extends reports whether Common adds anything to what is already typed.
func (r Result) Extends() bool { return len(r.Common) > len(r.Token.Text) }

// TokenAt finds the word the cursor sits in. cursor is a rune index.
//
// The line is scanned forward with quote state rather than backward from the
// cursor, because a quoted word may contain the spaces that a backward scan
// would mistake for its start.
func TokenAt(line string, cursor int) Token {
	r := []rune(line)
	if cursor > len(r) {
		cursor = len(r)
	}
	if cursor < 0 {
		cursor = 0
	}

	start := cursor
	var open rune      // the quote currently being scanned through
	var wordQuote rune // the quote that opened the current word, if any
	inWord := false

	for i := 0; i < cursor; i++ {
		c := r[i]
		switch {
		case open != 0:
			if c == open {
				open = 0
			}
		case c == '\'' || c == '"':
			if !inWord {
				start, inWord, wordQuote = i+1, true, c
			}
			open = c
		case c == ' ' || c == '\t':
			inWord, wordQuote = false, 0
		default:
			if !inWord {
				start, inWord, wordQuote = i, true, 0
			}
		}
	}
	if !inWord {
		start, wordQuote = cursor, 0
	}

	return Token{
		Text:  string(r[start:cursor]),
		Start: start,
		End:   cursor,
		Quote: byte(wordQuote),
	}
}

// Kind narrows what a completion may offer.
type Kind int

const (
	// Anything completes files and directories.
	Anything Kind = iota
	// DirsOnly completes directories, which is what `cd` wants.
	DirsOnly
)

// KindFor decides what the line is asking for. A line that starts with cd can
// only mean a directory.
func KindFor(line string) Kind {
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == "cd" || strings.HasPrefix(trimmed, "cd ") || strings.HasPrefix(trimmed, "cd\t") {
		return DirsOnly
	}
	return Anything
}

// maxCandidates caps a listing so completing at the root of a huge tree stays
// instant.
const maxCandidates = 300

// Paths completes tok against fsys. root is the session's directory; home is
// used for ~ and may be empty when the filesystem has no meaningful home.
func Paths(ctx context.Context, fsys vfs.FS, root, home string, tok Token, kind Kind) Result {
	res := Result{Token: tok}
	if fsys == nil {
		return res
	}

	word := tok.Text
	if home != "" && strings.HasPrefix(word, "~") {
		word = home + strings.TrimPrefix(word, "~")
	}

	// Split the word into the directory to list and the prefix to match.
	dir, prefix := "", word
	if i := strings.LastIndexByte(word, '/'); i >= 0 {
		dir, prefix = word[:i+1], word[i+1:]
	}

	listing := dir
	switch {
	case strings.HasPrefix(dir, "/"):
		// absolute, use as-is
	case dir == "":
		listing = root
	default:
		listing = vfs.Join(root, strings.TrimSuffix(dir, "/"))
	}

	entries, err := fsys.ReadDir(ctx, listing)
	if err != nil {
		return res
	}

	for _, e := range entries {
		if !strings.HasPrefix(e.Name, prefix) {
			continue
		}
		if kind == DirsOnly && !e.Dir {
			continue
		}
		// A leading dot is only offered when it was asked for, as in a shell.
		if strings.HasPrefix(e.Name, ".") && !strings.HasPrefix(prefix, ".") {
			continue
		}
		text := dir + e.Name
		if e.Dir {
			text += "/"
		}
		res.Candidates = append(res.Candidates, Candidate{
			Insert:  quote(text, tok.Quote),
			Display: e.Name + dirSuffix(e.Dir),
			Dir:     e.Dir,
		})
		if len(res.Candidates) >= maxCandidates {
			break
		}
	}

	sort.Slice(res.Candidates, func(i, j int) bool {
		a, b := res.Candidates[i], res.Candidates[j]
		if a.Dir != b.Dir {
			return a.Dir
		}
		return strings.ToLower(a.Display) < strings.ToLower(b.Display)
	})
	res.Common = commonPrefix(res.Candidates)
	return res
}

func dirSuffix(isDir bool) string {
	if isDir {
		return "/"
	}
	return ""
}

// quote wraps a completion so a path with spaces survives being typed back.
func quote(s string, opened byte) string {
	if opened != 0 {
		return s
	}
	if !strings.ContainsAny(s, " \t") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// commonPrefix is the longest prefix every candidate shares, which is what a
// single Tab inserts.
func commonPrefix(cs []Candidate) string {
	if len(cs) == 0 {
		return ""
	}
	p := cs[0].Insert
	for _, c := range cs[1:] {
		for !strings.HasPrefix(c.Insert, p) {
			p = p[:len(p)-1]
			if p == "" {
				return ""
			}
		}
	}
	return p
}

// Apply replaces the token in line with text and reports the new cursor index.
func Apply(line string, tok Token, text string) (string, int) {
	r := []rune(line)
	if tok.Start > len(r) {
		return line, len([]rune(line))
	}
	end := tok.End
	if end > len(r) {
		end = len(r)
	}
	out := string(r[:tok.Start]) + text + string(r[end:])
	return out, tok.Start + len([]rune(text))
}

// Names completes tok against a fixed list, which is how slash commands are
// offered. It shares the menu and cycling behaviour with path completion so
// there is only one thing for the user to learn.
func Names(all []string, tok Token) Result {
	res := Result{Token: tok}
	for _, n := range all {
		if !strings.HasPrefix(n, tok.Text) {
			continue
		}
		res.Candidates = append(res.Candidates, Candidate{Insert: n, Display: n})
	}
	sort.Slice(res.Candidates, func(i, j int) bool {
		return res.Candidates[i].Insert < res.Candidates[j].Insert
	})
	res.Common = commonPrefix(res.Candidates)
	return res
}
