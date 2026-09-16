package engine

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/shell"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// exitErr builds a real *exec.ExitError carrying the given status, through
// whichever shell this host actually has.
func exitErr(t *testing.T, code int) error {
	t.Helper()
	sh, args := shell.For(true)
	err := exec.Command(sh, append(args, "exit "+strconv.Itoa(code))...).Run()
	if err == nil {
		t.Fatalf("exit %d produced no error", code)
	}
	return err
}

func TestMissingBinaryFromExitCode(t *testing.T) {
	if !missingBinary(exitErr(t, 127), "") {
		t.Error("exit 127 was not read as a missing binary")
	}
	// An ordinary failure must not be mistaken for one: a test suite exiting 1
	// is the engine working, not the engine being absent.
	if missingBinary(exitErr(t, 1), "something went wrong") {
		t.Error("exit 1 was read as a missing binary")
	}
	if missingBinary(nil, "") {
		t.Error("a successful run was read as a missing binary")
	}
}

func TestMissingBinaryFromStderr(t *testing.T) {
	other := errors.New("exit status 1")
	for _, s := range []string{
		"/bin/bash: line 1: claude: command not found",
		`exec: "codex": executable file not found in $PATH`,
		"fork/exec /usr/bin/claude: no such file or directory",
	} {
		if !missingBinary(other, s) {
			t.Errorf("not recognised: %q", s)
		}
	}
	if missingBinary(other, "Error: invalid API key") {
		t.Error("an ordinary error was read as a missing binary")
	}
}

// TestMissingInNamesTheFilesystem is the point of the whole change: "claude:
// command not found" is baffling on a machine where claude is plainly
// installed, and stops being baffling once it says where it looked.
func TestMissingInNamesTheFilesystem(t *testing.T) {
	c := &CLI{id: IDClaude, bin: "claude"}

	msg := c.missingIn(vfs.NewWSL("Ubuntu-24.04"))
	if !strings.Contains(msg, "Ubuntu-24.04") {
		t.Errorf("the distribution is not named: %q", msg)
	}
	if !strings.Contains(msg, "claude") {
		t.Errorf("the binary is not named: %q", msg)
	}
	// It has to leave the user somewhere to go.
	if !strings.Contains(msg, "/engine api") {
		t.Errorf("no way out is offered: %q", msg)
	}

	// On the host there is no other filesystem to blame, so the message stays
	// plain rather than pointing at a distribution that is not involved.
	host := c.missingIn(vfs.NewLocal("."))
	if strings.Contains(host, "/engine api") || strings.Contains(host, "install it there") {
		t.Errorf("host message borrowed the remote advice: %q", host)
	}
	if !strings.Contains(host, "claude") {
		t.Errorf("host message does not name the binary: %q", host)
	}
}

// argvFor renders the command line claudeArgv builds for a mode.
func argvFor(mode agent.Mode, br *broker) string {
	c := &CLI{id: IDClaude, bin: "claude"}
	return strings.Join(claudeArgv(c, agent.Turn{Mode: mode, Prompt: "hi"}, br), " ")
}

// TestAutoDoesNotStallOnAPromptNobodyCanShow pins the setting measured against
// Claude Code 2.1.272: under -p with no permission tool, acceptEdits, auto and
// dontAsk all refuse a shell command and explain that they are waiting for an
// approval the session cannot display. Auto says it runs commands.
func TestAutoDoesNotStallOnAPromptNobodyCanShow(t *testing.T) {
	got := argvFor(agent.ModeAuto, nil)
	if !strings.Contains(got, "--permission-mode bypassPermissions") {
		t.Errorf("auto without a broker would stall: %s", got)
	}
	for _, blocked := range []string{"acceptEdits", "dontAsk"} {
		if strings.Contains(got, blocked) {
			t.Errorf("auto uses %s, which blocks Bash under -p: %s", blocked, got)
		}
	}
}

// TestAskWithoutABrokerStaysClosed is the other half: when asking was wanted
// and cannot happen, the answer is no, not "widen everything".
func TestAskWithoutABrokerStaysClosed(t *testing.T) {
	got := argvFor(agent.ModeAsk, nil)
	if strings.Contains(got, "bypassPermissions") || strings.Contains(got, "dangerously") {
		t.Errorf("ask mode widened itself when it could not ask: %s", got)
	}
	if !strings.Contains(got, "--permission-prompts none") {
		t.Errorf("ask mode did not close the door: %s", got)
	}
}

func TestPlanModeStaysReadOnly(t *testing.T) {
	got := argvFor(agent.ModePlan, nil)
	if !strings.Contains(got, "--permission-mode plan") {
		t.Errorf("plan mode = %s", got)
	}
	if strings.Contains(got, "bypassPermissions") || strings.Contains(got, "dangerously") {
		t.Errorf("plan mode was widened: %s", got)
	}
}
