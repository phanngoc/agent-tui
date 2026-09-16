package ui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/shell"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// A line starting with `!` is run rather than asked. It is the shell escape
// every terminal tool has, and it exists here for the same reason: half of what
// happens during a coding session is a command whose output you want the agent
// to have seen, and retyping it into a prompt as prose loses both the exactness
// and the output.
//
// The command runs wherever the session works — on the host, inside a
// container, inside a WSL distribution — and the result is appended to the
// transcript as a user message, so the next turn starts with it in context.
//
// Two commands are acted on rather than executed, because executing them could
// not do what they say. `cd` in a subshell moves a directory that exits with
// the subshell, and a bare `wsl` asks for an interactive login shell that has
// no terminal to attach to and would simply hang. Both are requests to move the
// session, so they move it: the tree, the preview, the index, search and the
// agent all follow, which is the whole point of typing them.

// maxBangOutput caps what a `!` command contributes, both to the transcript and
// to the agent's context. A build log can run to megabytes; the tail is where
// the failure is.
const maxBangOutput = 40 << 10

// bangTimeout stops a command that will never finish on its own. It matches the
// built-in agent's bash tool.
const bangTimeout = 60 * time.Second

// parseBang splits a `!` command line. ok is false for a bare `!` and for the
// `!!` escape, so a message that opens with an exclamation still reaches the
// agent — the same rule `//` follows for slash commands.
func parseBang(text string) (string, bool) {
	if !strings.HasPrefix(text, "!") || strings.HasPrefix(text, "!!") {
		return "", false
	}
	cmd := strings.TrimSpace(strings.TrimPrefix(text, "!"))
	if cmd == "" {
		return "", false
	}
	return cmd, true
}

// runBang acts on a `!` line: a move if it asks for one, otherwise a command.
func (m *Model) runBang(line string) tea.Cmd {
	if cmd, ok := m.bangMove(line); ok {
		return cmd
	}
	return m.startBang(line)
}

// bangHead splits the program from its arguments, ignoring the .exe a Windows
// user is as likely to type as not.
func bangHead(line string) (head string, rest []string) {
	f := strings.Fields(line)
	if len(f) == 0 {
		return "", nil
	}
	return strings.TrimSuffix(strings.ToLower(f[0]), ".exe"), f[1:]
}

// bangMove handles the lines that change where the session works. It reports
// false for everything else, which is then simply run.
func (m *Model) bangMove(line string) (tea.Cmd, bool) {
	head, rest := bangHead(line)
	switch head {
	case "cd":
		dir := "~"
		if len(rest) > 0 {
			dir = strings.Trim(strings.Join(rest, " "), `"'`)
		}
		return m.changeDir(dir), true

	case "wsl", "bash":
		// `wsl <command>` runs one command and comes straight back out; only
		// the forms that would have dropped you into a shell move the session.
		distro, enters := wslEntry(head, rest)
		if !enters {
			return nil, false
		}
		return m.enterWSL(distro), true

	case "exit", "logout":
		// `exit 3` is a command with a status in it; only a bare exit is the
		// gesture for leaving where the session is.
		if len(rest) > 0 {
			return nil, false
		}
		if t := m.mgr.Active().Target; t == "" || t == "host" {
			m.notice = "already on the host — /quit leaves agent-tui"
			return nil, true
		}
		return m.leaveTarget(), true

	case "host":
		return m.leaveTarget(), true
	}
	return nil, false
}

// wslEntry reads a `wsl` invocation and reports whether it asks for a shell
// rather than for one command. Only the distribution-selecting flags are
// understood; anything else is a command line and is left alone.
func wslEntry(head string, args []string) (distro string, enters bool) {
	if head == "bash" {
		// System32\bash.exe is the WSL launcher; `bash -c …` is a command.
		return "", len(args) == 0
	}
	for i := 0; i < len(args); i++ {
		switch strings.ToLower(args[i]) {
		case "-d", "--distribution":
			if i+1 >= len(args) {
				return "", false
			}
			distro = args[i+1]
			i++
		case "--cd", "-u", "--user":
			if i+1 >= len(args) {
				return "", false
			}
			i++ // a flag we do not act on, but whose value is not a command
		case "--shell-type":
			i++
		default:
			return "", false // a command to run, not a shell to enter
		}
	}
	return distro, true
}

// ---- moving into and out of a distribution ---------------------------------

// wslReadyMsg carries a resolved distribution back to the update loop.
type wslReadyMsg struct {
	fs  *vfs.WSL
	dir string
	err string
}

// enterWSL points the session at a WSL distribution, off the update loop.
//
// It lands where entering the distribution puts you — its login home — rather
// than carrying the host's directory across. A cold distribution takes seconds
// to boot, so the launch cannot happen inline without freezing the UI: it runs
// as a command and the switch lands as a message.
func (m *Model) enterWSL(distro string) tea.Cmd {
	s := m.mgr.Active()

	// Already there. Saying so beats a round trip that would end by throwing
	// away whatever directory the session had navigated to inside.
	if cur, ok := m.currentDistro(s); ok && (distro == "" || strings.EqualFold(distro, cur)) {
		m.notice = "already working in " + cur
		return nil
	}

	m.status = "starting " + wslStatusName(distro) + "…"

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()

		fs, dir, err := vfs.EnterWSL(ctx, distro)
		if err != nil {
			return wslReadyMsg{err: wslFailure(ctx, distro, err)}
		}
		return wslReadyMsg{fs: fs, dir: dir}
	}
}

// currentDistro reports the distribution a session is already working in.
func (m *Model) currentDistro(s *session.Session) (string, bool) {
	name, ok := strings.CutPrefix(s.Target, "wsl:")
	if !ok || name == "" {
		return "", false
	}
	return name, true
}

// wslFailure turns a failed launch into something actionable. The enumeration
// it costs is worth paying only here, on the path that has already failed:
// "no distribution called Ubunto" is worth a round trip, and naming the ones
// that do exist saves the next guess.
func wslFailure(ctx context.Context, distro string, err error) string {
	list := vfs.Distros(ctx)
	switch {
	case len(list) == 0:
		return "no WSL distributions are registered on this machine"
	case distro != "" && !knownDistro(list, distro):
		return "no WSL distribution called " + distro +
			" — try " + strings.Join(distroNames(list), ", ")
	}
	return err.Error()
}

func knownDistro(list []vfs.Distro, want string) bool {
	for _, d := range list {
		if strings.EqualFold(d.Name, want) {
			return true
		}
	}
	return false
}

func distroNames(list []vfs.Distro) []string {
	out := make([]string, len(list))
	for i, d := range list {
		out[i] = d.Name
	}
	return out
}

func wslStatusName(distro string) string {
	if distro == "" {
		return "WSL"
	}
	return distro
}

// applyWSLReady completes the switch begun by enterWSL.
func (m *Model) applyWSLReady(msg wslReadyMsg) tea.Cmd {
	m.status = ""
	if msg.err != "" {
		m.notice = msg.err
		return nil
	}
	m.fsCache[msg.fs.ID()] = msg.fs
	return m.useTarget(target{
		id:      msg.fs.ID(),
		label:   msg.fs.Label(),
		detail:  msg.dir,
		workdir: msg.dir,
		fs:      msg.fs,
	})
}

// leaveTarget returns the session to the host, landing on the Windows path for
// wherever it was when that path exists, so coming back out of a distribution
// is the move that going in was.
func (m *Model) leaveTarget() tea.Cmd {
	dir := m.hostFS.DefaultDir()
	if back, ok := vfs.WSLToHost(m.sessionCWD(m.mgr.Active())); ok {
		if st, err := m.hostFS.Stat(context.Background(), back); err == nil && st.Dir {
			dir = back
		}
	}
	return m.useTarget(target{
		id: "host", label: "host", detail: dir, workdir: dir, fs: m.hostFS,
	})
}

// ---- running a command -----------------------------------------------------

// bangDoneMsg carries a finished command back to the update loop.
type bangDoneMsg struct {
	sess    *session.Session
	index   int
	output  string
	exit    int
	elapsed time.Duration
}

// startBang appends the command to the transcript and runs it.
func (m *Model) startBang(line string) tea.Cmd {
	s := m.mgr.Active()
	fsys := m.sessionFS(s)
	dir := m.sessionCWD(s)

	s.Append(session.Message{
		Role: session.RoleUser,
		Text: line,
		Shell: &session.ShellRun{
			Command: line,
			Where:   fsys.Label(),
			Dir:     dir,
		},
	})
	m.mgr.Save(s)
	m.invalidateChat()

	index := len(s.Messages) - 1
	started := time.Now()

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), bangTimeout)
		defer cancel()

		sh, shArgs := shell.For(fsys.IsLocal())
		cmd := fsys.Command(ctx, dir, sh, append(shArgs, line)...)
		if fsys.IsLocal() {
			cmd.Env = append(os.Environ(), "TERM=dumb", "NO_COLOR=1", "CI=1")
		}
		out, err := cmd.CombinedOutput()

		text := string(out)
		if ctx.Err() == context.DeadlineExceeded {
			text += "\n(timed out after " + bangTimeout.String() + ")"
		}
		return bangDoneMsg{
			sess: s, index: index,
			output:  tailLines(text, maxBangOutput),
			exit:    bangExit(err),
			elapsed: time.Since(started),
		}
	}
}

// applyBangDone records the output against the message that started it.
//
// The message is found by index in its own session rather than by assuming the
// active one, because a command keeps running while you switch away from it.
func (m *Model) applyBangDone(msg bangDoneMsg) {
	s := msg.sess
	if s == nil || msg.index >= len(s.Messages) {
		return
	}
	run := s.Messages[msg.index].Shell
	if run == nil {
		return
	}
	run.Output, run.Exit, run.Elapsed, run.Done = msg.output, msg.exit, msg.elapsed, true

	// The agent reads Text, so the command and what it printed go there
	// together: seeing the output without seeing what produced it would be
	// worse than not seeing it at all.
	s.Messages[msg.index].Text = bangContext(run)
	s.Dirty = true
	m.mgr.Save(s)
	m.invalidateChat()
}

// bangContext is what the agent sees for a finished command.
func bangContext(run *session.ShellRun) string {
	var b strings.Builder
	b.WriteString("I ran this in " + run.Dir + ":\n\n$ " + run.Command + "\n")
	if out := strings.TrimRight(run.Output, "\n"); out != "" {
		b.WriteString(out + "\n")
	}
	if run.Exit != 0 {
		b.WriteString("(exit " + strconv.Itoa(run.Exit) + ")\n")
	}
	return b.String()
}

// bangExit reads a process result, reporting -1 for a command that never ran.
func bangExit(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// tailLines caps output at max bytes, keeping the end. A failure announces
// itself on the last lines, not the first.
func tailLines(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[len(s)-max:]
	if i := strings.IndexByte(s, '\n'); i >= 0 && i < len(s)-1 {
		s = s[i+1:]
	}
	return "… (earlier output dropped)\n" + s
}
