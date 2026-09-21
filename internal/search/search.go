// Package search does a ripgrep-shaped content scan over the indexed files.
//
// The scan is O(total bytes): each file is searched with a single forward walk
// and the line number is carried along incrementally rather than recomputed per
// hit. Work is spread over one goroutine per CPU.
package search

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Match is a single hit, reported at most once per line.
type Match struct {
	Path  string // relative, slash-separated
	Line  int    // 1-based
	Col   int    // 1-based byte column
	Text  string // the full line, with tabs expanded
	Start int    // byte offset of the match within Text
	End   int
}

// Options configures a scan.
type Options struct {
	Query         string
	Regex         bool
	CaseSensitive bool
	Limit         int   // stop after this many matches
	MaxFileBytes  int64 // skip anything larger
	Workers       int
}

func (o *Options) normalise() {
	if o.Limit <= 0 {
		o.Limit = 2000
	}
	if o.MaxFileBytes <= 0 {
		o.MaxFileBytes = 4 << 20
	}
	if o.Workers <= 0 {
		o.Workers = runtime.NumCPU()
	}
}

// Result carries everything the UI needs to render a scan.
type Result struct {
	Matches   []Match
	Files     int  // files that contained at least one match
	Scanned   int  // files actually read
	Truncated bool // the limit cut the result short
	Err       error
}

// Run scans files (relative to root) and returns the hits in path order.
func Run(ctx context.Context, root string, files []string, o Options) Result {
	o.normalise()
	if strings.TrimSpace(o.Query) == "" {
		return Result{}
	}

	// The query is compiled once here rather than per file, and the same
	// compiled value is what searching a conversation uses — so smart case and
	// the literal/regex split have one implementation, not two.
	mt, err := Compile(o)
	if err != nil {
		return Result{Err: err}
	}

	var (
		mu       sync.Mutex
		out      []Match
		scanned  int64
		hitFiles int64
		count    atomic.Int64
		stopped  atomic.Bool
	)

	jobs := make(chan string, o.Workers*8)
	var wg sync.WaitGroup

	worker := func() {
		defer wg.Done()
		// Its own scratch buffer: the compiled query is shared, the workspace
		// it lowers a file into is not.
		mine := mt.clone()
		for rel := range jobs {
			if stopped.Load() || ctx.Err() != nil {
				return
			}
			abs := filepath.Join(root, filepath.FromSlash(rel))
			st, err := os.Stat(abs)
			if err != nil || st.Size() == 0 || st.Size() > o.MaxFileBytes {
				continue
			}
			buf, err := os.ReadFile(abs)
			if err != nil {
				continue
			}
			atomic.AddInt64(&scanned, 1)
			if isBinary(buf) {
				continue
			}

			local := mine.Scan(rel, buf)
			if len(local) == 0 {
				continue
			}
			atomic.AddInt64(&hitFiles, 1)

			mu.Lock()
			out = append(out, local...)
			mu.Unlock()
			if count.Add(int64(len(local))) >= int64(o.Limit) {
				stopped.Store(true)
				return
			}
		}
	}

	wg.Add(o.Workers)
	for i := 0; i < o.Workers; i++ {
		go worker()
	}

feed:
	for _, f := range files {
		if stopped.Load() || ctx.Err() != nil {
			break feed
		}
		select {
		case jobs <- f:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()

	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})
	truncated := len(out) > o.Limit
	if truncated {
		out = out[:o.Limit]
	}
	return Result{
		Matches:   out,
		Files:     int(hitFiles),
		Scanned:   int(scanned),
		Truncated: truncated || stopped.Load(),
	}
}

// scanLiteral walks hay (already case-folded if needed) once. orig supplies the
// text reported back to the user so casing is preserved in the results.
func scanLiteral(rel string, orig, hay, pat []byte) []Match {
	var out []Match
	pos, line, lineStart := 0, 1, 0
	for pos <= len(hay)-len(pat) {
		idx := bytes.Index(hay[pos:], pat)
		if idx < 0 {
			break
		}
		at := pos + idx
		line, lineStart = advance(hay, lineStart, at, line)
		end := lineEnd(hay, at)
		out = append(out, mkMatch(rel, orig, line, lineStart, end, at, at+len(pat)))
		pos = end + 1 // one hit per line, like grep
	}
	return out
}

func scanRegex(rel string, buf []byte, re *regexp.Regexp) []Match {
	var out []Match
	pos, line, lineStart := 0, 1, 0
	for pos < len(buf) {
		loc := re.FindIndex(buf[pos:])
		if loc == nil {
			break
		}
		at := pos + loc[0]
		line, lineStart = advance(buf, lineStart, at, line)
		end := lineEnd(buf, at)
		out = append(out, mkMatch(rel, buf, line, lineStart, end, at, pos+loc[1]))
		if next := end + 1; next > pos {
			pos = next
		} else {
			pos++
		}
	}
	return out
}

// advance moves the line counter forward to the line containing at.
func advance(buf []byte, lineStart, at, line int) (int, int) {
	for i := lineStart; i < at; i++ {
		if buf[i] == '\n' {
			line++
			lineStart = i + 1
		}
	}
	return line, lineStart
}

func lineEnd(buf []byte, from int) int {
	if i := bytes.IndexByte(buf[from:], '\n'); i >= 0 {
		return from + i
	}
	return len(buf)
}

const maxLineLen = 400

func mkMatch(rel string, orig []byte, line, lineStart, lineStop, mStart, mEnd int) Match {
	if lineStop > len(orig) {
		lineStop = len(orig)
	}
	text := string(bytes.TrimRight(orig[lineStart:lineStop], "\r"))
	start, end := mStart-lineStart, mEnd-lineStart
	// Tab expansion shifts columns; do it before reporting offsets.
	if strings.IndexByte(text, '\t') >= 0 {
		text, start, end = expandTabs(text, start, end)
	}
	if len(text) > maxLineLen {
		text = text[:maxLineLen] + "…"
		start = min(start, maxLineLen)
		end = min(end, maxLineLen)
	}
	return Match{Path: rel, Line: line, Col: start + 1, Text: text, Start: start, End: end}
}

func expandTabs(s string, a, b int) (string, int, int) {
	var sb strings.Builder
	sb.Grow(len(s) + 8)
	na, nb := a, b
	col := 0
	for i := 0; i < len(s); i++ {
		if i == a {
			na = sb.Len()
		}
		if i == b {
			nb = sb.Len()
		}
		if s[i] == '\t' {
			pad := 4 - col%4
			for p := 0; p < pad; p++ {
				sb.WriteByte(' ')
			}
			col += pad
			continue
		}
		sb.WriteByte(s[i])
		col++
	}
	if b >= len(s) {
		nb = sb.Len()
	}
	return sb.String(), na, nb
}

func isBinary(b []byte) bool {
	if len(b) > 8192 {
		b = b[:8192]
	}
	return bytes.IndexByte(b, 0) >= 0
}

func asciiLowerCopy(b []byte) []byte {
	out := make([]byte, len(b))
	asciiLowerInto(out, b)
	return out
}

// asciiLowerInto lowercases ASCII only, so byte offsets are preserved exactly.
func asciiLowerInto(dst, src []byte) {
	for i := 0; i < len(src); i++ {
		c := src[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		dst[i] = c
	}
}
