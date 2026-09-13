// Package preview loads a file once, highlights it, and keeps the result in a
// small LRU. Re-opening a file the user just looked at is free, which is what
// makes arrow-key browsing through search results feel instant.
package preview

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/phanngoc/agent-tui/internal/highlight"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// File is a loaded, highlighted document.
type File struct {
	Abs       string
	Rel       string
	Lang      string
	Styled    []string // ANSI-coloured, one entry per source line
	Plain     []string // same lines without styling, for in-file search
	Size      int64
	Truncated bool
	Binary    bool
	Err       error
}

// LineCount is the number of lines actually loaded.
func (f *File) LineCount() int {
	if f == nil {
		return 0
	}
	return len(f.Styled)
}

type entry struct {
	key  string
	file *File
}

// Loader caches highlighted files keyed by path, size and mtime.
type Loader struct {
	sc       *highlight.Scheme
	maxBytes int64
	capacity int

	mu    sync.Mutex
	items map[string]*entry
	order []string // least-recently-used first
}

func NewLoader(sc *highlight.Scheme, maxKB, capacity int) *Loader {
	if maxKB <= 0 {
		maxKB = 2048
	}
	if capacity <= 0 {
		capacity = 16
	}
	return &Loader{
		sc:       sc,
		maxBytes: int64(maxKB) << 10,
		capacity: capacity,
		items:    make(map[string]*entry, capacity),
	}
}

// Load returns the highlighted file, hitting the cache when the file has not
// changed since it was last read. fsys is where the file lives: the host, or a
// container the session is pointed at.
func (l *Loader) Load(fsys vfs.FS, abs, rel string) *File {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	st, err := fsys.Stat(ctx, abs)
	if err != nil {
		return &File{Abs: abs, Rel: rel, Err: err}
	}
	if st.Dir {
		return &File{Abs: abs, Rel: rel, Err: fmt.Errorf("is a directory")}
	}
	// The filesystem id is part of the key: the same path means different
	// files on the host and inside a container.
	key := fmt.Sprintf("%s|%s|%d|%d", fsys.ID(), abs, st.Size, st.ModNano)

	l.mu.Lock()
	if e, ok := l.items[key]; ok {
		l.touch(key)
		l.mu.Unlock()
		return e.file
	}
	l.mu.Unlock()

	f := l.read(ctx, fsys, abs, rel, st.Size)

	l.mu.Lock()
	l.items[key] = &entry{key: key, file: f}
	l.order = append(l.order, key)
	for len(l.order) > l.capacity {
		oldest := l.order[0]
		l.order = l.order[1:]
		delete(l.items, oldest)
	}
	l.mu.Unlock()
	return f
}

func (l *Loader) touch(key string) {
	for i, k := range l.order {
		if k == key {
			l.order = append(l.order[:i], l.order[i+1:]...)
			break
		}
	}
	l.order = append(l.order, key)
}

func (l *Loader) read(ctx context.Context, fsys vfs.FS, abs, rel string, size int64) *File {
	f := &File{Abs: abs, Rel: rel, Size: size}

	buf, truncated, err := fsys.ReadFile(ctx, abs, l.maxBytes)
	if err != nil {
		f.Err = err
		return f
	}
	f.Truncated = truncated

	if IsBinary(buf) {
		f.Binary = true
		f.Lang = "binary"
		f.Styled = hexDump(buf)
		f.Plain = f.Styled
		return f
	}

	// A truncation must not land mid-line or mid-rune.
	if f.Truncated {
		if i := bytes.LastIndexByte(buf, '\n'); i > 0 {
			buf = buf[:i]
		}
		for len(buf) > 0 && !utf8.Valid(buf) {
			buf = buf[:len(buf)-1]
		}
	}

	lang := highlight.Detect(rel)
	f.Lang = lang.Name
	f.Styled = highlight.Render(l.sc, lang, buf)
	// Plain is derived from Styled rather than from the raw bytes so the two
	// always agree character-for-character. In-file search maps byte offsets in
	// Plain straight onto the viewport's highlight ranges, which the viewport
	// computes against the ANSI-stripped content.
	f.Plain = stripLines(f.Styled)
	return f
}

func stripLines(styled []string) []string {
	out := make([]string, len(styled))
	for i, s := range styled {
		out[i] = ansi.Strip(s)
	}
	return out
}

// IsBinary uses the heuristic git uses: a NUL byte in the first 8KB.
func IsBinary(b []byte) bool {
	if len(b) > 8192 {
		b = b[:8192]
	}
	return bytes.IndexByte(b, 0) >= 0
}

func hexDump(b []byte) []string {
	const perLine = 16
	if len(b) > 64*1024 {
		b = b[:64*1024]
	}
	out := make([]string, 0, len(b)/perLine+1)
	var sb strings.Builder
	for off := 0; off < len(b); off += perLine {
		end := min(off+perLine, len(b))
		sb.Reset()
		fmt.Fprintf(&sb, "%08x  ", off)
		for i := off; i < off+perLine; i++ {
			if i < end {
				fmt.Fprintf(&sb, "%02x ", b[i])
			} else {
				sb.WriteString("   ")
			}
			if i-off == 7 {
				sb.WriteByte(' ')
			}
		}
		sb.WriteString(" |")
		for i := off; i < end; i++ {
			c := b[i]
			if c < 0x20 || c > 0x7e {
				c = '.'
			}
			sb.WriteByte(c)
		}
		sb.WriteByte('|')
		out = append(out, sb.String())
	}
	return out
}
