package ui

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

func TestParseBang(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"!ls", "ls", true},
		{"! ls -la", "ls -la", true},
		{"!go test ./...", "go test ./...", true},
		{"!", "", false},
		{"!  ", "", false},
		// The escape, so a prompt may still open with an exclamation.
		{"!! this is a question", "", false},
		{"ls", "", false},
		{"what does ! mean", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := parseBang(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("parseBang(%q) = %q,%v want %q,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestWSLEntryDistinguishesShellFromCommand(t *testing.T) {
	cases := []struct {
		line   string
		distro string
		enters bool
	}{
		// Forms that would have opened a shell: these move the session.
		{"wsl", "", true},
		{"wsl -d Ubuntu-24.04", "Ubuntu-24.04", true},
		{"wsl --distribution Debian", "Debian", true},
		{"wsl -u root", "", true},
		{"bash", "", true},
		// Forms that run something and come back: these are just commands.
		{"wsl ls -la", "", false},
		{"wsl -d Ubuntu-24.04 uname -a", "Ubuntu-24.04", false},
		{"wsl --list --verbose", "", false},
		{"wsl -e /bin/sh", "", false},
		{"bash -c 'echo hi'", "", false},
		{"wsl -d", "", false}, // a flag with nothing after it
	}
	for _, c := range cases {
		head, rest := bangHead(c.line)
		distro, enters := wslEntry(head, rest)
		if enters != c.enters {
			t.Errorf("%q: enters = %v, want %v", c.line, enters, c.enters)
			continue
		}
		if enters && distro != c.distro {
			t.Errorf("%q: distro = %q, want %q", c.line, distro, c.distro)
		}
	}
}

func TestBangHeadIgnoresExeSuffix(t *testing.T) {
	for _, line := range []string{"wsl.exe", "WSL.EXE", "wsl"} {
		if head, _ := bangHead(line); head != "wsl" {
			t.Errorf("bangHead(%q) = %q, want wsl", line, head)
		}
	}
}

func TestKnownDistro(t *testing.T) {
	list := []vfs.Distro{{Name: "Debian"}, {Name: "Ubuntu-24.04", Default: true}}
	if !knownDistro(list, "debian") {
		t.Error("a name should match case-insensitively")
	}
	if knownDistro(list, "Fedora") {
		t.Error("an unregistered name was accepted")
	}
}

// TestEnterWSLWhenAlreadyThereDoesNothing keeps a second !wsl from throwing
// away wherever the session had navigated to inside the distribution.
func TestEnterWSLWhenAlreadyThereDoesNothing(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	s.Target, s.CWD = "wsl:Ubuntu-24.04", "/mnt/c/src/app"

	if cmd := m.enterWSL(""); cmd != nil {
		t.Error("!wsl inside a distribution started another launch")
	}
	if m.notice == "" {
		t.Error("nothing said the session was already there")
	}
	if s.CWD != "/mnt/c/src/app" {
		t.Errorf("the session moved to %q", s.CWD)
	}
	// Naming a different distribution is a real move and must still go ahead.
	if cmd := m.enterWSL("Debian"); cmd == nil {
		t.Error("!wsl -d Debian from inside Ubuntu did nothing")
	}
}

func TestTailLinesKeepsTheEnd(t *testing.T) {
	if got := tailLines("short", 100); got != "short" {
		t.Errorf("a short string was rewritten: %q", got)
	}
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		b.WriteString("line of output here\n")
	}
	b.WriteString("THE FAILURE\n")

	got := tailLines(b.String(), 1024)
	if len(got) > 1024+64 {
		t.Errorf("output not capped: %d bytes", len(got))
	}
	if !strings.Contains(got, "THE FAILURE") {
		t.Error("the tail, which is where the failure is, was dropped")
	}
	if !strings.HasPrefix(got, "…") {
		t.Errorf("nothing said output was dropped: %q", got[:20])
	}
	// The cut must land on a line boundary rather than mid-line.
	body := strings.SplitN(got, "\n", 2)[1]
	if !strings.HasPrefix(body, "line of output here") {
		t.Errorf("the cut left a partial line: %q", body[:30])
	}
}

func TestBangContextShowsCommandAndOutput(t *testing.T) {
	run := &session.ShellRun{
		Command: "go test ./...", Dir: "/app", Output: "FAIL\tpkg\n", Exit: 1, Done: true,
	}
	got := bangContext(run)
	for _, want := range []string{"go test ./...", "FAIL\tpkg", "/app", "exit 1"} {
		if !strings.Contains(got, want) {
			t.Errorf("context is missing %q:\n%s", want, got)
		}
	}
	// A clean run says nothing about its exit code.
	ok := bangContext(&session.ShellRun{Command: "true", Dir: "/app", Done: true})
	if strings.Contains(ok, "exit") {
		t.Errorf("a successful command reported an exit code:\n%s", ok)
	}
}

// TestBangRunsRatherThanAsks is the whole point of the feature: the line must
// reach a shell, not the agent.
func TestBangRunsRatherThanAsks(t *testing.T) {
	m := newTestModel(t)

	m.input.SetValue("!echo hello-from-bang")
	cmd := m.inputKey(key("enter"))
	if cmd == nil {
		t.Fatal("a ! line produced no command")
	}
	if m.input.Value() != "" {
		t.Errorf("the input was not cleared: %q", m.input.Value())
	}

	s := m.mgr.Active()
	if len(s.Messages) != 1 {
		t.Fatalf("want one message, got %d", len(s.Messages))
	}
	msg := s.Messages[0]
	if msg.Shell == nil {
		t.Fatal("the message is not marked as a command")
	}
	if msg.Shell.Command != "echo hello-from-bang" {
		t.Errorf("command = %q", msg.Shell.Command)
	}
	if msg.Shell.Done {
		t.Error("the command was marked finished before it ran")
	}
	if s.Busy {
		t.Error("a ! command put the session in a turn")
	}

	done := runUntil[bangDoneMsg](t, cmd)
	m.Update(done)

	run := m.mgr.Active().Messages[0].Shell
	if !run.Done {
		t.Fatal("the command never completed")
	}
	if run.Exit != 0 {
		t.Errorf("exit = %d, output %q", run.Exit, run.Output)
	}
	if !strings.Contains(run.Output, "hello-from-bang") {
		t.Errorf("output = %q", run.Output)
	}
	// The agent reads Text, and must see both halves.
	text := m.mgr.Active().Messages[0].Text
	if !strings.Contains(text, "echo hello-from-bang") || !strings.Contains(text, "hello-from-bang") {
		t.Errorf("the agent would not see the command and its output: %q", text)
	}
}

func TestBangReportsAFailingCommand(t *testing.T) {
	m := newTestModel(t)

	m.input.SetValue("!exit 3")
	cmd := m.inputKey(key("enter"))
	m.Update(runUntil[bangDoneMsg](t, cmd))

	run := m.mgr.Active().Messages[0].Shell
	if run.Exit != 3 {
		t.Errorf("exit = %d, want 3 (output %q)", run.Exit, run.Output)
	}
	if !strings.Contains(m.mgr.Active().Messages[0].Text, "exit 3") {
		t.Error("the agent would not see that the command failed")
	}
}

// TestBangRunsInTheSessionDirectory guards the thing that makes `!` worth
// having over a second terminal: it runs where the agent runs.
func TestBangRunsInTheSessionDirectory(t *testing.T) {
	m := newTestModel(t)
	root := m.idx.Root()

	m.input.SetValue("cd internal")
	m.Update(runUntil[indexReadyMsg](t, m.inputKey(key("enter"))))

	m.input.SetValue("!pwd")
	cmd := m.inputKey(key("enter"))
	m.Update(runUntil[bangDoneMsg](t, cmd))

	msgs := m.mgr.Active().Messages
	run := msgs[len(msgs)-1].Shell
	if run.Dir != filepath.Join(root, "internal") {
		t.Errorf("recorded dir = %q, want %q", run.Dir, filepath.Join(root, "internal"))
	}
	// pwd prints it too, whichever shell ran; compare loosely, because a POSIX
	// shell on Windows answers in its own path syntax.
	if !strings.Contains(strings.ToLower(strings.ReplaceAll(run.Output, "\\", "/")),
		"internal") {
		t.Errorf("the command did not run in the session directory: %q", run.Output)
	}
}

func TestBangEscapeStillReachesTheAgent(t *testing.T) {
	m := newTestModel(t)
	m.input.SetValue("!! is this a factorial")
	m.inputKey(key("enter"))

	s := m.mgr.Active()
	if len(s.Messages) != 1 {
		t.Fatalf("want one message, got %d", len(s.Messages))
	}
	if s.Messages[0].Shell != nil {
		t.Error("the escape was run as a command")
	}
	if s.Messages[0].Text != "!! is this a factorial" {
		t.Errorf("text = %q", s.Messages[0].Text)
	}
}

// TestBangCDMovesTheSession covers the reason cd is intercepted: run in a
// subshell it would move a directory that dies with the subshell.
func TestBangCDMovesTheSession(t *testing.T) {
	m := newTestModel(t)
	root := m.idx.Root()

	m.input.SetValue("!cd internal")
	cmd := m.inputKey(key("enter"))
	if cmd == nil {
		t.Fatal("!cd produced no command")
	}
	m.Update(runUntil[indexReadyMsg](t, cmd))

	want := filepath.Join(root, "internal")
	if got := m.mgr.Active().CWD; got != want {
		t.Errorf("session cwd = %q, want %q", got, want)
	}
	if m.tree.Root() != want {
		t.Errorf("the tree did not follow: %q", m.tree.Root())
	}
	// It moved the session rather than running a shell, and it said so: a
	// directory change every later path depends on is part of the conversation.
	msgs := m.mgr.Active().Messages
	if len(msgs) != 1 || msgs[0].Shell == nil {
		t.Fatalf("!cd left %d messages: %+v", len(msgs), msgs)
	}
	run := msgs[0].Shell
	if !run.Done || run.Exit != 0 {
		t.Errorf("the move is recorded as unfinished or failed: %+v", run)
	}
	if !strings.HasPrefix(run.Command, "cd ") || !strings.Contains(run.Command, want) {
		t.Errorf("the record does not say where it went: %q", run.Command)
	}
	if m.mgr.Active().Busy {
		t.Error("!cd started a turn")
	}
}

func TestBangExitOnTheHostSaysSo(t *testing.T) {
	m := newTestModel(t)
	m.input.SetValue("!exit")
	m.inputKey(key("enter"))

	if len(m.mgr.Active().Messages) != 0 {
		t.Error("!exit on the host ran a command")
	}
	if m.notice == "" {
		t.Error("nothing explained that there was nothing to exit")
	}
}

// wslOrSkip skips a test wherever WSL is not usable.
func wslOrSkip(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("WSL is a Windows host feature")
	}
	if len(vfs.Distros(context.Background())) == 0 {
		t.Skip("no WSL distributions registered")
	}
}

// TestBangWSLMovesTheWholeSession is the feature in one test: typing !wsl must
// move everything that shows or touches files, not just the shell that runs the
// next command. An explorer still listing the host while the agent edits files
// inside a distribution is worse than no feature at all — it shows one
// filesystem and describes another.
func TestBangWSLMovesTheWholeSession(t *testing.T) {
	wslOrSkip(t)
	m := newTestModel(t)
	hostRoot := m.idx.Root()

	m.input.SetValue("!wsl")
	cmd := m.inputKey(key("enter"))
	if cmd == nil {
		t.Fatal("!wsl produced no command")
	}
	ready := runUntil[wslReadyMsg](t, cmd)
	if ready.err != "" {
		t.Skipf("WSL is registered but not startable here: %s", ready.err)
	}
	_, next := m.Update(ready)
	if next == nil {
		t.Fatal("the switch produced no follow-up work")
	}
	m.Update(runUntil[indexReadyMsg](t, next))

	s := m.mgr.Active()
	if !strings.HasPrefix(s.Target, "wsl:") {
		t.Fatalf("session target = %q, want a wsl: id", s.Target)
	}
	if f := m.tree.FS(); f == nil || f.IsLocal() {
		t.Fatal("the tree is still reading the host filesystem")
	}
	if m.idx.Root() != m.tree.Root() {
		t.Errorf("the index (%q) and the tree (%q) disagree", m.idx.Root(), m.tree.Root())
	}
	if !strings.HasPrefix(m.tree.Root(), "/") {
		t.Errorf("the tree root %q is not a path inside the distribution", m.tree.Root())
	}
	if m.sessionCWD(s) != m.tree.Root() {
		t.Errorf("the agent would run in %q while the tree shows %q",
			m.sessionCWD(s), m.tree.Root())
	}
	// Entering lands in the distribution's own home, not on the host's
	// directory carried across as a /mnt path.
	if strings.HasPrefix(m.tree.Root(), "/mnt/") {
		t.Errorf("landed on the host's directory at %q, want the distribution's home",
			m.tree.Root())
	}
	_ = hostRoot
	// And the tree must actually be able to read it.
	if !strings.Contains(m.explorerTitle(), ":") {
		t.Errorf("the pane title does not name the distribution: %q", m.explorerTitle())
	}

	// Coming back out is the same move in reverse.
	m.input.SetValue("!exit")
	back := m.inputKey(key("enter"))
	if back == nil {
		t.Fatal("!exit produced no command")
	}
	m.Update(runUntil[indexReadyMsg](t, back))

	if got := m.mgr.Active().Target; got != "host" {
		t.Errorf("target after !exit = %q, want host", got)
	}
	if m.tree.Root() != hostRoot {
		t.Errorf("came back to %q, want %q", m.tree.Root(), hostRoot)
	}
	if f := m.tree.FS(); f == nil || !f.IsLocal() {
		t.Error("the tree did not return to the host filesystem")
	}
}

// TestBangWSLLandsInTheDistributionHome pins where entering puts you: the
// distribution's own home, the way opening it in a terminal does. It used to
// translate the session's Windows directory into /mnt and land there, which
// kept a host checkout on screen and hid the Linux work that was the reason to
// go in at all.
func TestBangWSLLandsInTheDistributionHome(t *testing.T) {
	wslOrSkip(t)
	m := newTestModel(t)

	// Narrow the session first, so a landing that carried the host's directory
	// across would be visibly different from the home.
	m.input.SetValue("cd internal")
	m.Update(runUntil[indexReadyMsg](t, m.inputKey(key("enter"))))

	m.input.SetValue("!wsl")
	ready := runUntil[wslReadyMsg](t, m.inputKey(key("enter")))
	if ready.err != "" {
		t.Skipf("WSL is registered but not startable here: %s", ready.err)
	}
	_, next := m.Update(ready)
	m.Update(runUntil[indexReadyMsg](t, next))

	home := ready.fs.DefaultDir()
	if m.tree.Root() != home {
		t.Errorf("explorer is at %q, want the home at %q", m.tree.Root(), home)
	}
	if strings.HasPrefix(m.tree.Root(), "/mnt/") {
		t.Errorf("landed on the host's directory at %q", m.tree.Root())
	}
	// Everything that reads files has to agree on it, or the explorer is
	// describing a directory nothing else is using.
	if m.sessionCWD(m.mgr.Active()) != m.tree.Root() {
		t.Errorf("the prompt would run in %q while the explorer shows %q",
			m.sessionCWD(m.mgr.Active()), m.tree.Root())
	}
	if m.idx.Root() != m.tree.Root() {
		t.Errorf("the finder is at %q while the explorer shows %q",
			m.idx.Root(), m.tree.Root())
	}
}

// TestEnterWSLIsOneLaunch pins the cost of the move. It used to enumerate,
// health-check, probe the home directory and stat the project separately —
// four round trips at about a quarter of a second each, all of them spent
// showing the directory the user had just left.
func TestEnterWSLIsOneLaunch(t *testing.T) {
	wslOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Warm the distribution, so this times the move and not a cold boot.
	if _, _, err := vfs.EnterWSL(ctx, ""); err != nil {
		t.Skipf("WSL not startable here: %v", err)
	}

	start := time.Now()
	fs, dir, err := vfs.EnterWSL(ctx, "")
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if fs.Distro() == "" {
		t.Error("the distribution did not name itself")
	}
	if !strings.HasPrefix(dir, "/") || strings.HasPrefix(dir, "/mnt/") {
		t.Errorf("landed in %q, want a home inside the distribution", dir)
	}
	if fs.DefaultDir() != dir {
		t.Errorf("the probed home %q was not kept (DefaultDir says %q)", dir, fs.DefaultDir())
	}
	// One warm launch is a few hundred milliseconds; three would not fit.
	if took > 2*time.Second {
		t.Errorf("entering took %s, which is more than one launch", took)
	}
	t.Logf("entered %s at %s in %s", fs.Distro(), dir, took.Round(time.Millisecond))
}

// TestBangRunsInsideWSLOnceThere checks the other half: after the move, a `!`
// command must run in the distribution rather than back on the host.
func TestBangRunsInsideWSLOnceThere(t *testing.T) {
	wslOrSkip(t)
	m := newTestModel(t)

	ready := runUntil[wslReadyMsg](t, m.enterWSL(""))
	if ready.err != "" {
		t.Skipf("WSL is registered but not startable here: %s", ready.err)
	}
	_, next := m.Update(ready)
	m.Update(runUntil[indexReadyMsg](t, next))

	m.input.SetValue("!uname -s")
	cmd := m.inputKey(key("enter"))
	m.Update(runUntil[bangDoneMsg](t, cmd))

	msgs := m.mgr.Active().Messages
	run := msgs[len(msgs)-1].Shell
	if run == nil || !run.Done {
		t.Fatal("the command did not finish")
	}
	if !strings.Contains(run.Output, "Linux") {
		t.Errorf("the command ran on the host, not in WSL: %q", run.Output)
	}
	if !strings.HasPrefix(run.Dir, "/") {
		t.Errorf("recorded directory %q is not inside the distribution", run.Dir)
	}
}

// TestShellBlockRenders guards the transcript path: a command must not be
// drawn as if the user had said it.
func TestShellBlockRenders(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	s.Append(session.Message{
		Role: session.RoleUser,
		Text: "ls",
		Shell: &session.ShellRun{
			Command: "ls", Where: "host", Dir: "/app",
			Output: "main.go\ngo.mod\n", Exit: 0, Elapsed: 12 * time.Millisecond, Done: true,
		},
	})
	m.invalidateChat()

	out := stripANSI(m.transcript(80))
	for _, want := range []string{"host $", "ls", "main.go", "go.mod"} {
		if !strings.Contains(out, want) {
			t.Errorf("transcript is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "you") {
		t.Errorf("a command was drawn as a user message:\n%s", out)
	}
}

func TestShellBlockShowsFailure(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	s.Append(session.Message{
		Role: session.RoleUser, Text: "false",
		Shell: &session.ShellRun{Command: "false", Where: "host", Exit: 1, Done: true},
	})
	m.invalidateChat()

	out := stripANSI(m.transcript(80))
	if !strings.Contains(out, "exit 1") {
		t.Errorf("a failing command did not say so:\n%s", out)
	}
	if !strings.Contains(out, "(no output)") {
		t.Errorf("a silent command left an empty block:\n%s", out)
	}
}

func TestShellBlockShowsRunning(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	s.Append(session.Message{
		Role: session.RoleUser, Text: "sleep 10",
		Shell: &session.ShellRun{Command: "sleep 10", Where: "host"},
	})
	m.invalidateChat()

	if out := stripANSI(m.transcript(80)); !strings.Contains(out, "running") {
		t.Errorf("an unfinished command looked finished:\n%s", out)
	}
}

// TestShellRunDoesNotTitleTheSession keeps the session list readable: a
// session named "ls" says nothing about what it is for.
func TestShellRunDoesNotTitleTheSession(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	s.Append(session.Message{
		Role: session.RoleUser, Text: "ls",
		Shell: &session.ShellRun{Command: "ls", Where: "host"},
	})
	if s.Title != "" {
		t.Errorf("a command named the session %q", s.Title)
	}
	s.Append(session.Message{Role: session.RoleUser, Text: "make the ranking shorter"})
	if s.Title == "" {
		t.Error("a real prompt should still name the session")
	}
}
