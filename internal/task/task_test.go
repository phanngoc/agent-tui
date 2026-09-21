package task

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/shell"
)

func waitFor(t *testing.T, r *Registry, cond func() bool, what string) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		if cond() {
			return
		}
		select {
		case <-r.Changed():
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func TestStartCapturesOutputAndFinishes(t *testing.T) {
	r := NewRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := shCmd(ctx, "echo one; echo two >&2; echo three")

	tk := r.Start("", "echo test", cmd, cancel, "sess-1")
	if tk.State() != Running {
		t.Fatalf("state = %s, want running", tk.State())
	}
	waitFor(t, r, func() bool { return !tk.Live() }, "the task to finish")

	if tk.State() != Done {
		t.Errorf("state = %s, want done", tk.State())
	}
	got := strings.Join(tk.Output(), "\n")
	for _, want := range []string{"one", "two", "three"} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q is missing %q — stderr should be captured too", got, want)
		}
	}
	if tk.Elapsed() <= 0 {
		t.Error("elapsed time was not recorded")
	}
}

func TestFailureIsRecorded(t *testing.T) {
	r := NewRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	tk := r.Start("", "false", shCmd(ctx, "echo bad; exit 3"), cancel, "s")

	waitFor(t, r, func() bool { return !tk.Live() }, "the task to fail")
	if tk.State() != Failed {
		t.Errorf("state = %s, want failed", tk.State())
	}
	if tk.Exit() != 3 {
		t.Errorf("exit = %d, want 3", tk.Exit())
	}
}

func TestStopEndsARunningTask(t *testing.T) {
	r := NewRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	tk := r.Start("", "sleep", shCmd(ctx, "sleep 30"), cancel, "s")

	tk.Stop()
	waitFor(t, r, func() bool { return !tk.Live() }, "the task to stop")
	if tk.Live() {
		t.Errorf("state = %s, want it finished", tk.State())
	}
}

func TestOutputIsBounded(t *testing.T) {
	r := NewRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	tk := r.Start("", "flood", shCmd(ctx,
		"i=0; while [ $i -lt 3000 ]; do echo line$i; i=$((i+1)); done"), cancel, "s")

	waitFor(t, r, func() bool { return !tk.Live() }, "the flood to finish")
	if n := len(tk.Output()); n > maxLines {
		t.Errorf("kept %d lines, want at most %d", n, maxLines)
	}
	// The tail is what survives, because that is what matters.
	if last := tk.Output()[len(tk.Output())-1]; last != "line2999" {
		t.Errorf("last line = %q, want the most recent", last)
	}
}

func TestAdoptAndUpdateForeignTasks(t *testing.T) {
	// Claude Code reports its own background work; it lands in the same list.
	r := NewRegistry()
	tk := r.Adopt("bbkhka38p", "npm test", "sess-1")
	if tk.State() != Running || tk.Owner != "sess-1" {
		t.Fatalf("adopted task = %+v", tk)
	}
	if again := r.Adopt("bbkhka38p", "npm test", "sess-1"); again != tk {
		t.Error("adopting the same id twice created a second task")
	}

	r.Update("bbkhka38p", Done, "42 passing")
	if tk.State() != Done || tk.Ended().IsZero() {
		t.Errorf("task = %+v", tk)
	}
	if got := strings.Join(tk.Output(), "\n"); !strings.Contains(got, "42 passing") {
		t.Errorf("output = %q", got)
	}
	r.Update("no-such-task", Done, "") // must not panic
}

func TestLiveCount(t *testing.T) {
	r := NewRegistry()
	r.Adopt("a", "one", "s")
	r.Adopt("b", "two", "s")
	if got := r.LiveCount(); got != 2 {
		t.Errorf("live = %d, want 2", got)
	}
	r.Update("a", Done, "")
	if got := r.LiveCount(); got != 1 {
		t.Errorf("live = %d, want 1", got)
	}
}

func TestTailIsSafeToHold(t *testing.T) {
	r := NewRegistry()
	tk := r.Adopt("a", "one", "s")
	tk.append("first")
	held := tk.Output()
	tk.append("second")
	if len(held) != 1 {
		t.Error("a held slice changed underneath the caller")
	}
	if got := tk.Tail(1); len(got) != 1 || got[0] != "second" {
		t.Errorf("tail = %v", got)
	}
}

// shCmd builds a command through whichever shell this machine has, so these
// tests exercise the registry rather than the presence of /bin/sh.
func shCmd(ctx context.Context, command string) *exec.Cmd {
	name, args := shell.For(true)
	return exec.CommandContext(ctx, name, append(args, command)...)
}
