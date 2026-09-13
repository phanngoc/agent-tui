package vfs

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/phanngoc/agent-tui/internal/ignore"
)

// Docker reads the filesystem inside a running container.
//
// Every operation is a `docker exec` of a command chosen to behave identically
// under BusyBox and GNU coreutils, because a container is as likely to be
// Alpine as Debian. That rules out conveniences like `stat --printf`, `ls
// --time-style` and `grep --exclude-dir`, none of which BusyBox implements.
type Docker struct {
	container string // name or id
	image     string
	workdir   string

	mu      sync.Mutex
	ignored *ignore.Set
	igRoot  string
}

// NewDocker targets a running container. workdir is where new sessions start,
// falling back to / when the image declares none.
func NewDocker(container, image, workdir string) *Docker {
	if workdir == "" {
		workdir = "/"
	}
	return &Docker{container: container, image: image, workdir: workdir}
}

func (d *Docker) ID() string         { return "docker:" + d.container }
func (d *Docker) Label() string      { return d.container }
func (d *Docker) IsLocal() bool      { return false }
func (d *Docker) DefaultDir() string { return d.workdir }
func (d *Docker) Image() string      { return d.image }
func (d *Docker) Container() string  { return d.container }

// Health reports whether the container is still running, which is the failure
// everything else would otherwise surface as a confusing exec error.
func (d *Docker) Health(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, "docker", "inspect",
		"-f", "{{.State.Running}}", d.container).Output()
	if err != nil {
		return fmt.Errorf("container %s is not reachable", d.container)
	}
	if strings.TrimSpace(string(out)) != "true" {
		return fmt.Errorf("container %s is not running", d.container)
	}
	return nil
}

// exec runs a shell snippet inside the container and returns its stdout.
func (d *Docker) sh(ctx context.Context, dir, script string) ([]byte, error) {
	args := []string{"exec"}
	if dir != "" {
		args = append(args, "-w", dir)
	}
	args = append(args, d.container, "sh", "-c", script)

	cmd := exec.CommandContext(ctx, "docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.Bytes(), fmt.Errorf("%s", msg)
	}
	return stdout.Bytes(), nil
}

// listFormat is the one stat invocation that works on both BusyBox and GNU.
const listFormat = `'%F|%s|%Y|%n'`

func (d *Docker) ReadDir(ctx context.Context, dir string) ([]DirEntry, error) {
	script := fmt.Sprintf(
		`find %s -maxdepth 1 -mindepth 1 -exec stat -c %s {} + 2>/dev/null`,
		shellQuote(dir), listFormat)
	out, err := d.sh(ctx, "", script)
	if err != nil && len(out) == 0 {
		return nil, err
	}
	return parseStatLines(out, dir), nil
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

// WriteFile streams the contents in over stdin, which keeps it binary-safe and
// avoids quoting the body into a shell command.
func (d *Docker) WriteFile(ctx context.Context, path string, data []byte) error {
	script := fmt.Sprintf(`mkdir -p %s && cat > %s`,
		shellQuote(Dir(path)), shellQuote(path))

	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", d.container, "sh", "-c", script)
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return err
	}
	return nil
}

func (d *Docker) Stat(ctx context.Context, path string) (FileInfo, error) {
	out, err := d.sh(ctx, "", fmt.Sprintf(`stat -c %s %s`, listFormat, shellQuote(path)))
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

func (d *Docker) ReadFile(ctx context.Context, path string, max int64) ([]byte, bool, error) {
	script := fmt.Sprintf(`cat %s`, shellQuote(path))
	if max > 0 {
		// One byte past the cap, so truncation is detectable without a stat.
		script = fmt.Sprintf(`head -c %d %s`, max+1, shellQuote(path))
	}
	out, err := d.sh(ctx, "", script)
	if err != nil {
		return nil, false, err
	}
	if max > 0 && int64(len(out)) > max {
		return out[:max], true, nil
	}
	return out, false, nil
}

// ListFiles walks inside the container. Pruning happens there rather than here
// so the noisy directories are never descended into or sent over the wire.
func (d *Docker) ListFiles(ctx context.Context, root string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 200000
	}
	script := fmt.Sprintf(`%s -type f -print 2>/dev/null | head -n %d`,
		findPrefix(root), limit)
	out, err := d.sh(ctx, "", script)
	if err != nil && len(out) == 0 {
		return nil, err
	}

	ig := d.ignoreFor(ctx, root)
	lines := strings.Split(string(out), "\n")
	paths := make([]string, 0, len(lines))
	for _, p := range lines {
		if p == "" {
			continue
		}
		rel := Rel(root, p)
		if rel == p || rel == "" || ig.Match(rel, false) {
			continue
		}
		paths = append(paths, rel)
	}
	sortShortestFirst(paths)
	return paths, nil
}

// Grep searches inside the container. `find -print0 | xargs -0 grep` is used
// instead of `grep -r --exclude-dir` because BusyBox grep has no --exclude-dir.
func (d *Docker) Grep(ctx context.Context, root string, o GrepOptions) ([]GrepHit, bool, error) {
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

	out, err := d.sh(ctx, "", script)
	if err != nil && len(out) == 0 {
		// grep exits non-zero when nothing matched; that is not an error.
		return nil, false, nil
	}

	ig := d.ignoreFor(ctx, root)
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

// ignoreFor caches the container's .gitignore for a root. Reading it costs a
// docker exec, and it is consulted on every listing.
func (d *Docker) ignoreFor(ctx context.Context, root string) *ignore.Set {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ignored != nil && d.igRoot == root {
		return d.ignored
	}
	set := ignore.Empty()
	if out, err := d.sh(ctx, "", fmt.Sprintf(`cat %s 2>/dev/null`,
		shellQuote(Join(root, ".gitignore")))); err == nil {
		set = ignore.Parse(string(out))
	}
	d.ignored, d.igRoot = set, root
	return set
}

// Command runs a program inside the container, so an agent CLI executes next to
// the files it is editing.
func (d *Docker) Command(ctx context.Context, dir, name string, args ...string) *exec.Cmd {
	full := []string{"exec", "-i"}
	if dir != "" {
		full = append(full, "-w", dir)
	}
	full = append(full, d.container, name)
	full = append(full, args...)
	return exec.CommandContext(ctx, "docker", full...)
}

// shellQuote wraps a value in single quotes for `sh -c`.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// dirMarker separates the per-directory sections of a batched listing.
const dirMarker = "\x01DIR\x01"

// ReadDirs lists every directory in one exec. A tree refresh touches each open
// directory, and at roughly a quarter of a second per `docker exec` doing them
// one at a time would put the tree visibly behind the filesystem.
func (d *Docker) ReadDirs(ctx context.Context, dirs []string) map[string][]DirEntry {
	out := make(map[string][]DirEntry, len(dirs))
	if len(dirs) == 0 {
		return out
	}

	var sb strings.Builder
	for _, dir := range dirs {
		fmt.Fprintf(&sb, "printf '%%s\\n' %s; find %s -maxdepth 1 -mindepth 1 -exec stat -c %s {} + 2>/dev/null; ",
			shellQuote(dirMarker+dir), shellQuote(dir), listFormat)
	}

	raw, err := d.sh(ctx, "", sb.String())
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
