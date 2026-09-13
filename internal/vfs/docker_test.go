package vfs

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// dockerOrSkip finds a running container to test against. These tests read only
// and are skipped wherever Docker is not available.
func dockerOrSkip(t *testing.T) *Docker {
	t.Helper()
	if os.Getenv("AGENT_TUI_DOCKER") != "1" {
		t.Skip("set AGENT_TUI_DOCKER=1 to test against real containers")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not installed")
	}
	list := Containers(context.Background())
	if len(list) == 0 {
		t.Skip("no running containers")
	}
	c := list[0]
	return NewDocker(c.Name, c.Image, c.Workdir)
}

func TestDockerReadDir(t *testing.T) {
	d := dockerOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	entries, err := d.ReadDir(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]DirEntry{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	// Every Linux image has these, and etc must come back as a directory with
	// a plausible mtime.
	for _, want := range []string{"etc", "tmp"} {
		e, ok := byName[want]
		if !ok {
			t.Fatalf("/%s missing from %v", want, byName)
		}
		if !e.Dir {
			t.Errorf("/%s is not reported as a directory", want)
		}
		if e.ModNano <= 0 {
			t.Errorf("/%s has no mtime", want)
		}
	}
	if _, ok := byName[""]; ok {
		t.Error("an empty name leaked into the listing")
	}
}

func TestDockerReadFileIsByteExact(t *testing.T) {
	d := dockerOrSkip(t)
	ctx := context.Background()

	full, _, err := d.ReadFile(ctx, "/etc/hostname", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(full) == 0 {
		t.Fatal("/etc/hostname came back empty")
	}

	// Truncation must stop exactly at the cap and say so.
	head, truncated, err := d.ReadFile(ctx, "/etc/hostname", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(head) != 3 {
		t.Errorf("read %d bytes, want 3", len(head))
	}
	if !truncated {
		t.Error("truncation was not reported")
	}
	if string(head) != string(full[:3]) {
		t.Errorf("truncated read %q does not match the start of %q", head, full)
	}
}

func TestDockerStat(t *testing.T) {
	d := dockerOrSkip(t)
	ctx := context.Background()

	dir, err := d.Stat(ctx, "/etc")
	if err != nil || !dir.Dir {
		t.Fatalf("/etc stat = %+v, %v", dir, err)
	}
	file, err := d.Stat(ctx, "/etc/hostname")
	if err != nil || file.Dir || file.Size == 0 {
		t.Fatalf("/etc/hostname stat = %+v, %v", file, err)
	}
	if _, err := d.Stat(ctx, "/definitely/not/here"); err == nil {
		t.Error("a missing path should report an error")
	}
}

func TestDockerListFiles(t *testing.T) {
	d := dockerOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	files, err := d.ListFiles(ctx, "/etc", 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no files found under /etc")
	}
	for _, f := range files {
		if strings.HasPrefix(f, "/") {
			t.Fatalf("paths must be relative to the root, got %q", f)
		}
	}
}

func TestDockerGrep(t *testing.T) {
	d := dockerOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	hits, _, err := d.Grep(ctx, "/etc", GrepOptions{Query: "root", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no matches for 'root' anywhere under /etc")
	}
	for _, h := range hits {
		if h.Line <= 0 || h.Path == "" || strings.HasPrefix(h.Path, "/") {
			t.Fatalf("malformed hit %+v", h)
		}
	}

	// A query that cannot match must come back empty rather than as an error:
	// grep exits non-zero when it finds nothing.
	none, _, err := d.Grep(ctx, "/etc", GrepOptions{Query: "zzz-no-such-string-zzz", Limit: 20})
	if err != nil {
		t.Fatalf("an empty result should not be an error: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("unexpected hits: %+v", none)
	}
}

func TestDockerHealth(t *testing.T) {
	d := dockerOrSkip(t)
	if err := d.Health(context.Background()); err != nil {
		t.Errorf("a running container reported unhealthy: %v", err)
	}
	gone := NewDocker("agent-tui-no-such-container", "", "/")
	if gone.Health(context.Background()) == nil {
		t.Error("a missing container should report unhealthy")
	}
}

func TestParseGrepLineHandlesColons(t *testing.T) {
	// Paths and text both routinely contain colons.
	hit, ok := parseGrepLine("/root/a:b.go:42:map[string]:int{}", "/root")
	if !ok {
		t.Fatal("failed to parse")
	}
	if hit.Path != "a:b.go" || hit.Line != 42 || hit.Text != "map[string]:int{}" {
		t.Errorf("hit = %+v", hit)
	}
	if _, ok := parseGrepLine("no colons here", "/root"); ok {
		t.Error("a line with no location should not parse")
	}
}

func TestParseStatLinesSkipsSpecialFiles(t *testing.T) {
	out := []byte(strings.Join([]string{
		"directory|4096|1700000000|/x/etc",
		"regular file|12|1700000001|/x/a.go",
		"symbolic link|7|1700000002|/x/link",
		"character special file|0|1700000003|/x/null",
		"regular empty file|0|1700000004|/x/empty",
		"malformed line",
	}, "\n"))

	got := parseStatLines(out, "/x")
	if len(got) != 3 {
		t.Fatalf("kept %d entries, want 3 (dir, file, empty file): %+v", len(got), got)
	}
	if !got[0].Dir || got[0].Name != "etc" {
		t.Errorf("first entry = %+v", got[0])
	}
	if got[1].Name != "a.go" || got[1].Size != 12 {
		t.Errorf("second entry = %+v", got[1])
	}
	if got[1].ModNano != 1700000001*int64(time.Second) {
		t.Errorf("mtime was not converted to nanoseconds: %d", got[1].ModNano)
	}
}

func TestShellQuoteEscapes(t *testing.T) {
	for in, want := range map[string]string{
		"plain":      "'plain'",
		"with space": "'with space'",
		"it's":       `'it'\''s'`,
		"$(rm -rf)":  "'$(rm -rf)'",
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestDockerReadDirsBatches(t *testing.T) {
	d := dockerOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dirs := []string{"/", "/etc", "/definitely-not-here"}
	got := ReadDirs(ctx, d, dirs)

	if len(got) != len(dirs) {
		t.Fatalf("got %d sections for %d directories: %v", len(got), len(dirs), got)
	}
	if len(got["/"]) == 0 || len(got["/etc"]) == 0 {
		t.Errorf("real directories came back empty: %v", got)
	}
	if len(got["/definitely-not-here"]) != 0 {
		t.Errorf("a missing directory should list nothing, got %v", got["/definitely-not-here"])
	}

	// The batched result must agree with reading one at a time.
	single, err := d.ReadDir(ctx, "/etc")
	if err != nil {
		t.Fatal(err)
	}
	if len(single) != len(got["/etc"]) {
		t.Errorf("batched listing has %d entries, single read has %d",
			len(got["/etc"]), len(single))
	}
}

func TestDockerReadDirsIsOneRoundTrip(t *testing.T) {
	d := dockerOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	dirs := []string{"/", "/etc", "/usr", "/var", "/tmp"}

	start := time.Now()
	ReadDirs(ctx, d, dirs)
	batched := time.Since(start)

	start = time.Now()
	for _, dir := range dirs {
		_, _ = d.ReadDir(ctx, dir)
	}
	oneByOne := time.Since(start)

	if batched > oneByOne {
		t.Errorf("batching %d directories (%v) was slower than %d separate reads (%v)",
			len(dirs), batched, len(dirs), oneByOne)
	}
	t.Logf("batched %v vs one-by-one %v for %d directories", batched, oneByOne, len(dirs))
}
