package vfs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/phanngoc/agent-tui/internal/ignore"
)

// posixFS is a filesystem reached by running shell scripts somewhere else.
//
// Two backends work this way — a container over `docker exec`, and a WSL
// distribution over `wsl.exe` — and the only thing that differs between them is
// how a script gets launched. Everything above that is identical, so it lives
// here once: the same listing, the same stat format, the same find-and-grep.
//
// The scripts are written to behave identically under BusyBox and GNU
// coreutils, because a container is as likely to be Alpine as Debian. That
// rules out conveniences like `stat --printf`, `ls --time-style` and `grep
// --exclude-dir`, none of which BusyBox implements.
type posixFS struct {
	run launcher

	mu      sync.Mutex
	ignored *ignore.Set
	igRoot  string
}

// launcher runs a shell script in the namespace the filesystem lives in.
type launcher interface {
	// sh runs a script and returns its stdout. dir is the working directory,
	// or "" for the namespace's own default.
	sh(ctx context.Context, dir, script string) ([]byte, error)
	// shIn runs a script with data on its stdin, discarding stdout.
	shIn(ctx context.Context, script string, stdin io.Reader) error
}

// listFormat is the one stat invocation that works on both BusyBox and GNU.
const listFormat = `'%F|%s|%Y|%n'`

// dirMarker separates the per-directory sections of a batched listing.
const dirMarker = "\x01DIR\x01"

func (p *posixFS) ReadDir(ctx context.Context, dir string) ([]DirEntry, error) {
	script := fmt.Sprintf(
		`find %s -maxdepth 1 -mindepth 1 -exec stat -c %s {} + 2>/dev/null`,
		shellQuote(dir), listFormat)
	out, err := p.run.sh(ctx, "", script)
	if err != nil && len(out) == 0 {
		return nil, err
	}
	return parseStatLines(out, dir), nil
}

// ReadDirs lists every directory in one launch. A tree refresh touches each
// open directory, and at roughly a quarter of a second per launch doing them
// one at a time would put the tree visibly behind the filesystem.
func (p *posixFS) ReadDirs(ctx context.Context, dirs []string) map[string][]DirEntry {
	out := make(map[string][]DirEntry, len(dirs))
	if len(dirs) == 0 {
		return out
	}

	var sb strings.Builder
	for _, dir := range dirs {
		fmt.Fprintf(&sb, "printf '%%s\\n' %s; find %s -maxdepth 1 -mindepth 1 -exec stat -c %s {} + 2>/dev/null; ",
			shellQuote(dirMarker+dir), shellQuote(dir), listFormat)
	}

	raw, err := p.run.sh(ctx, "", sb.String())
	if err != nil && len(raw) == 0 {
		for _, dir := range dirs {
			out[dir] = nil
		}
		return out
	}

	// Split the stream back into per-directory sections.
	cur := ""
	var buf []byte
	flush := func() {
		if cur != "" {
			out[cur] = parseStatLines(buf, cur)
		}
		buf = buf[:0]
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if rest, ok := strings.CutPrefix(line, dirMarker); ok {
			flush()
			cur = rest
			continue
		}
		buf = append(buf, line...)
		buf = append(buf, '\n')
	}
	flush()

	for _, dir := range dirs {
		if _, ok := out[dir]; !ok {
			out[dir] = nil
		}
	}
	return out
}

// parseStatLines turns `type|size|mtime|path` lines into entries.
func parseStatLines(out []byte, dir string) []DirEntry {
	lines := strings.Split(string(out), "\n")
	entries := make([]DirEntry, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		// The path may itself contain '|', so split only the three leading
		// fields off the front.
		parts := strings.SplitN(line, "|", 4)
		if len(parts) != 4 {
			continue
		}
		kind := parts[0]
		isDir := kind == "directory"
		if !isDir && kind != "regular file" && kind != "regular empty file" {
			continue
		}
		size, _ := strconv.ParseInt(parts[1], 10, 64)
		mod, _ := strconv.ParseInt(parts[2], 10, 64)
		name := Base(parts[3])
		if name == "" || parts[3] == dir {
			continue
		}
		entries = append(entries, DirEntry{
			Name: name, Dir: isDir, Size: size, ModNano: mod * int64(time.Second),
		})
	}
	return entries
}

func (p *posixFS) Stat(ctx context.Context, path string) (FileInfo, error) {
	out, err := p.run.sh(ctx, "", fmt.Sprintf(`stat -c %s %s`, listFormat, shellQuote(path)))
	if err != nil {
		return FileInfo{}, err
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "|", 4)
	if len(parts) != 4 {
		return FileInfo{}, fmt.Errorf("cannot stat %s", path)
	}
	size, _ := strconv.ParseInt(parts[1], 10, 64)
	mod, _ := strconv.ParseInt(parts[2], 10, 64)
	return FileInfo{
		Size:    size,
		Dir:     parts[0] == "directory",
		ModNano: mod * int64(time.Second),
	}, nil
}

func (p *posixFS) ReadFile(ctx context.Context, path string, max int64) ([]byte, bool, error) {
	script := fmt.Sprintf(`cat %s`, shellQuote(path))
	if max > 0 {
		// One byte past the cap, so truncation is detectable without a stat.
		script = fmt.Sprintf(`head -c %d %s`, max+1, shellQuote(path))
	}
	out, err := p.run.sh(ctx, "", script)
	if err != nil {
		return nil, false, err
	}
	if max > 0 && int64(len(out)) > max {
		return out[:max], true, nil
	}
	return out, false, nil
}

// WriteFile streams the contents in over stdin, which keeps it binary-safe and
// avoids quoting the body into a shell command.
func (p *posixFS) WriteFile(ctx context.Context, path string, data []byte) error {
	script := fmt.Sprintf(`mkdir -p %s && cat > %s`,
		shellQuote(Dir(path)), shellQuote(path))
	return p.run.shIn(ctx, script, bytes.NewReader(data))
}

// ListFiles walks on the far side. Pruning happens there rather than here so
// the noisy directories are never descended into or sent over the wire.
func (p *posixFS) ListFiles(ctx context.Context, root string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 200000
	}
	script := fmt.Sprintf(`%s -type f -print 2>/dev/null | head -n %d`,
		findPrefix(root), limit)
	out, err := p.run.sh(ctx, "", script)
	if err != nil && len(out) == 0 {
		return nil, err
	}

	ig := p.ignoreFor(ctx, root)
	lines := strings.Split(string(out), "\n")
	paths := make([]string, 0, len(lines))
	for _, f := range lines {
		if f == "" {
			continue
		}
		rel := Rel(root, f)
		if rel == f || rel == "" || ig.Match(rel, false) {
			continue
		}
		paths = append(paths, rel)
	}
	sortShortestFirst(paths)
	return paths, nil
}

// Grep searches on the far side. `find -print0 | xargs -0 grep` is used instead
// of `grep -r --exclude-dir` because BusyBox grep has no --exclude-dir.
func (p *posixFS) Grep(ctx context.Context, root string, o GrepOptions) ([]GrepHit, bool, error) {
	if strings.TrimSpace(o.Query) == "" {
		return nil, false, nil
	}
	limit := o.Limit
	if limit <= 0 {
		limit = 2000
	}

	flags := "-In"
	if o.Regex {
		flags += "E"
	} else {
		flags += "F"
	}
	// Smart case, matching the local backend.
	if !o.CaseSensitive && strings.ToLower(o.Query) == o.Query {
		flags += "i"
	}

	script := fmt.Sprintf(`%s -type f -print0 2>/dev/null | xargs -0 -r grep %s -e %s 2>/dev/null | head -n %d`,
		findPrefix(root), flags, shellQuote(o.Query), limit+1)

	out, err := p.run.sh(ctx, "", script)
	if err != nil && len(out) == 0 {
		// grep exits non-zero when nothing matched; that is not an error.
		return nil, false, nil
	}

	ig := p.ignoreFor(ctx, root)
	lines := strings.Split(string(out), "\n")
	hits := make([]GrepHit, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		hit, ok := parseGrepLine(line, root, o.Query)
		if !ok || ig.Match(hit.Path, false) {
			continue
		}
		hits = append(hits, hit)
	}
	truncated := len(hits) > limit
	if truncated {
		hits = hits[:limit]
	}
	return hits, truncated, nil
}

// parseGrepLine splits `path:line:text`. Paths can contain colons, so the line
// number is found by scanning for the first all-digit field after a colon.
func parseGrepLine(line, root, needle string) (GrepHit, bool) {
	for i := 0; i < len(line); i++ {
		if line[i] != ':' {
			continue
		}
		rest := line[i+1:]
		j := strings.IndexByte(rest, ':')
		if j <= 0 {
			continue
		}
		n, err := strconv.Atoi(rest[:j])
		if err != nil {
			continue
		}
		path := Rel(root, line[:i])
		if path == line[:i] {
			continue // outside the root
		}
		text := rest[j+1:]
		if len(text) > 400 {
			text = text[:400] + "…"
		}
		hit := GrepHit{Path: path, Line: n, Text: text}
		// grep gives no column, so the match is located here.
		if at := strings.Index(text, needle); at >= 0 && needle != "" {
			hit.Start, hit.End = at, at+len(needle)
		}
		return hit, true
	}
	return GrepHit{}, false
}

// findPrefix builds the pruning part of a find invocation, shared by the file
// walk and the content search.
func findPrefix(root string) string {
	var names []string
	for _, d := range ignore.PruneDirs() {
		names = append(names, fmt.Sprintf("-name %s", shellQuote(d)))
	}
	return fmt.Sprintf(`find %s \( %s \) -prune -o`,
		shellQuote(root), strings.Join(names, " -o "))
}

// ignoreFor caches the far side's .gitignore for a root. Reading it costs a
// process launch, and it is consulted on every listing.
func (p *posixFS) ignoreFor(ctx context.Context, root string) *ignore.Set {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ignored != nil && p.igRoot == root {
		return p.ignored
	}
	set := ignore.Empty()
	if out, err := p.run.sh(ctx, "", fmt.Sprintf(`cat %s 2>/dev/null`,
		shellQuote(Join(root, ".gitignore")))); err == nil {
		set = ignore.Parse(string(out))
	}
	p.ignored, p.igRoot = set, root
	return set
}

// shellQuote wraps a value in single quotes for `sh -c`.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
