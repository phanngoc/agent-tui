package vfs

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

// utf16le encodes the way wsl.exe writes to a redirected pipe.
func utf16le(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 0, len(u)*2)
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return b
}

func TestDecodeUTF16(t *testing.T) {
	const msg = "There is no distribution with the supplied name.\r\n"
	if got := decodeUTF16(utf16le(msg)); got != msg {
		t.Errorf("no BOM: got %q want %q", got, msg)
	}
	if got := decodeUTF16(append([]byte{0xFF, 0xFE}, utf16le(msg)...)); got != msg {
		t.Errorf("with BOM: got %q want %q", got, msg)
	}
	// Plain UTF-8 must survive untouched: it is what the Linux side writes,
	// and mangling it would corrupt every file this package reads.
	for _, s := range []string{"", "hello", "ls: /nope: No such file", "héllo ✓", "a"} {
		if got := decodeUTF16([]byte(s)); got != s {
			t.Errorf("utf-8 %q became %q", s, got)
		}
	}
}

func TestDecodeUTF16LeavesBinaryAlone(t *testing.T) {
	// A file with NUL bytes is read through the same path as a listing; it
	// must come back byte for byte rather than being read as UTF-16.
	bin := []byte{0x7f, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00}
	if got := decodeUTF16(bin); got != string(bin) {
		t.Errorf("binary was rewritten: %q", got)
	}
}

func TestParseDistros(t *testing.T) {
	const out = "  NAME            STATE           VERSION\r\n" +
		"* Ubuntu-24.04    Running         2\r\n" +
		"  Debian          Stopped         2\r\n" +
		"  My Distro 1     Stopped         2\r\n"

	got := parseDistros(out)
	want := []Distro{
		{Name: "Ubuntu-24.04", State: "Running", Default: true},
		{Name: "Debian", State: "Stopped"},
		{Name: "My Distro 1", State: "Stopped"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d distros, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("distro %d: got %+v want %+v", i, got[i], want[i])
		}
	}
}

func TestParseDistrosEmpty(t *testing.T) {
	if got := parseDistros(""); len(got) != 0 {
		t.Errorf("got %+v, want none", got)
	}
	if got := parseDistros("Windows Subsystem for Linux has no installed distributions.\r\n"); len(got) != 0 {
		t.Errorf("got %+v, want none", got)
	}
}

func TestHostToWSL(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{`C:\Users\me\src`, "/mnt/c/Users/me/src", true},
		{`c:\src`, "/mnt/c/src", true},
		{`D:\`, "/mnt/d", true},
		{`D:`, "/mnt/d", true},
		{`C:/Users/me`, "/mnt/c/Users/me", true},
		{`\\server\share`, "", false},
		{"/home/me", "", false},
	}
	for _, c := range cases {
		got, ok := HostToWSL(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("HostToWSL(%q) = %q,%v want %q,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestWSLToHost(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"/mnt/c/Users/me/src", `C:\Users\me\src`, true},
		{"/mnt/d", `D:\`, true},
		{"/mnt/d/", `D:\`, true},
		{"/home/me", "", false},
		{"/mnt/wsl/stuff", "", false},
		{"/mnt/", "", false},
	}
	for _, c := range cases {
		got, ok := WSLToHost(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("WSLToHost(%q) = %q,%v want %q,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestPathRoundTrip(t *testing.T) {
	for _, p := range []string{`C:\Users\me\src`, `E:\a\b\c`} {
		w, ok := HostToWSL(p)
		if !ok {
			t.Fatalf("HostToWSL(%q) failed", p)
		}
		back, ok := WSLToHost(w)
		if !ok || back != p {
			t.Errorf("%q -> %q -> %q,%v", p, w, back, ok)
		}
	}
}

// wslOrSkip returns a filesystem for the default distribution, skipping
// wherever WSL is not available. These tests only read.
func wslOrSkip(t *testing.T) *WSL {
	t.Helper()
	if _, err := exec.LookPath("wsl.exe"); err != nil {
		t.Skip("wsl.exe is not installed")
	}
	list := Distros(context.Background())
	if len(list) == 0 {
		t.Skip("no WSL distributions registered")
	}
	name := list[0].Name
	for _, d := range list {
		if d.Default {
			name = d.Name
		}
	}
	w := NewWSL(name)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := w.Health(ctx); err != nil {
		t.Skipf("distribution not startable: %v", err)
	}
	return w
}

func TestWSLReadDir(t *testing.T) {
	w := wslOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	entries, err := w.ReadDir(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]DirEntry{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	for _, want := range []string{"etc", "usr", "mnt"} {
		e, ok := byName[want]
		if !ok {
			t.Fatalf("/%s missing from listing of /: %v", want, byName)
		}
		if !e.Dir {
			t.Errorf("/%s is not reported as a directory", want)
		}
	}
}

func TestWSLReadDirsBatched(t *testing.T) {
	w := wslOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dirs := []string{"/", "/etc", "/nonexistent-dir-xyz"}
	got := ReadDirs(ctx, w, dirs)
	if len(got) != len(dirs) {
		t.Fatalf("got %d sections, want %d", len(got), len(dirs))
	}
	if len(got["/etc"]) == 0 {
		t.Error("/etc came back empty")
	}
	if len(got["/nonexistent-dir-xyz"]) != 0 {
		t.Error("a missing directory produced entries")
	}
}

func TestWSLReadFileAndStat(t *testing.T) {
	w := wslOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	info, err := w.Stat(ctx, "/etc/hostname")
	if err != nil {
		t.Fatal(err)
	}
	if info.Dir {
		t.Error("/etc/hostname reported as a directory")
	}

	data, truncated, err := w.ReadFile(ctx, "/etc/hostname", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Error("a short file was reported truncated")
	}
	if strings.TrimSpace(string(data)) == "" {
		t.Error("/etc/hostname read back empty")
	}

	// Truncation must be reported, and must not silently decode as UTF-16.
	short, truncated, err := w.ReadFile(ctx, "/etc/hostname", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(short) != 1 {
		t.Errorf("cap of 1 byte gave %d bytes, truncated=%v", len(short), truncated)
	}
}

func TestWSLGrep(t *testing.T) {
	w := wslOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	hits, _, err := w.Grep(ctx, "/etc", GrepOptions{Query: "root", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no matches for 'root' anywhere under /etc")
	}
	for _, h := range hits {
		if strings.HasPrefix(h.Path, "/") {
			t.Errorf("hit path %q is not relative to the root", h.Path)
		}
		if h.Line <= 0 {
			t.Errorf("hit %q has line %d", h.Path, h.Line)
		}
	}
}

func TestWSLDefaultDirIsPosix(t *testing.T) {
	w := wslOrSkip(t)
	home := w.DefaultDir()
	if !strings.HasPrefix(home, "/") {
		t.Errorf("home %q is not a POSIX path", home)
	}
	if home != w.DefaultDir() {
		t.Error("the cached home changed between calls")
	}
}

func TestWSLHealthRejectsMissingDistro(t *testing.T) {
	if _, err := exec.LookPath("wsl.exe"); err != nil {
		t.Skip("wsl.exe is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err := NewWSL("no-such-distro-xyz").Health(ctx)
	if err == nil {
		t.Fatal("a missing distribution reported healthy")
	}
	// The message comes from wsl.exe as UTF-16; if it were not decoded it
	// would arrive as NUL-interleaved mojibake.
	if strings.ContainsRune(err.Error(), 0) {
		t.Errorf("undecoded UTF-16 in error: %q", err.Error())
	}
}

func TestShellJoinQuotes(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"claude", "--version"}, `'claude' '--version'`},
		{[]string{"/home/me/my tools/bin/x", "-p"}, `'/home/me/my tools/bin/x' '-p'`},
		{[]string{"echo", "it's"}, `'echo' 'it'\''s'`},
	}
	for _, c := range cases {
		if got := shellJoin(c.in); got != c.want {
			t.Errorf("shellJoin(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

// TestWSLCommandSeesTheUserPath is the bug this guards: wsl.exe runs a command
// with a bare PATH that omits ~/.local/bin, so a CLI installed there reports
// "command not found" in a distribution where it is plainly installed.
func TestWSLCommandSeesTheUserPath(t *testing.T) {
	w := wslOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// A marker only a login shell would have on its PATH.
	dir := "$HOME/.local/bin"
	if _, err := w.sh(ctx, "", `mkdir -p ~/.local/bin && printf '#!/bin/sh\necho on-user-path\n' > ~/.local/bin/agent-tui-probe && chmod +x ~/.local/bin/agent-tui-probe`); err != nil {
		t.Skipf("cannot write a probe into %s: %v", dir, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = w.sh(ctx, "", `rm -f ~/.local/bin/agent-tui-probe`)
	})

	out, err := w.Command(ctx, "/", "agent-tui-probe").CombinedOutput()
	if err != nil {
		t.Fatalf("a binary in ~/.local/bin was not found: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "on-user-path") {
		t.Errorf("output = %q", out)
	}
}

// TestWSLCommandCarriesArgumentsIntact keeps the login shell from mangling what
// it is given: the command line is now built by quoting rather than passed as
// an argv, so a space or a quote in an argument is the thing to get wrong.
func TestWSLCommandCarriesArgumentsIntact(t *testing.T) {
	w := wslOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// $HOME and $(...) are the ones that matter: a prompt reaches the CLI on
	// its command line, so anything re-parsed by a shell over there is
	// executed with whatever the user typed.
	for _, arg := range []string{
		"plain", "two words", "it's", "a|b", "$HOME", "x y 'z'",
		"$(echo pwned)", "`echo pwned`", "a\b", `say "hi"`, "semi;colon",
	} {
		out, err := w.Command(ctx, "/", "printf", "%s", arg).CombinedOutput()
		if err != nil {
			t.Fatalf("printf %q: %v\n%s", arg, err, out)
		}
		if string(out) != arg {
			t.Errorf("argument %q came back as %q", arg, out)
		}
	}
}
