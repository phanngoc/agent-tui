//go:build windows

package term

import (
	"strings"
	"testing"
	"time"
)

// A shell in a pseudo-console takes input and prints; a page attaching later
// is replayed what it printed; its exit is seen.
func TestTerminalRunsAndReplays(t *testing.T) {
	m := NewManager()
	defer m.CloseAll()
	tm, err := m.Start(Spec{Root: "r", Dir: t.TempDir(), Shell: "cmd", Argv: []string{"cmd.exe", "/Q", "/K"}, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	_, out, detach := tm.Attach()
	defer detach()
	if err := tm.Write([]byte("echo agent-tui-%USERDOMAIN%-ok\r")); err != nil {
		t.Fatal(err)
	}
	var got strings.Builder
	deadline := time.After(15 * time.Second)
	for !strings.Contains(got.String(), "agent-tui-") || !strings.Contains(got.String(), "-ok") {
		select {
		case b, ok := <-out:
			if !ok {
				t.Fatalf("the terminal ended early: %q", got.String())
			}
			got.Write(b)
		case <-deadline:
			t.Fatalf("no echo; got %q", got.String())
		}
	}
	replay, _, d2 := tm.Attach()
	d2()
	if !strings.Contains(string(replay), "-ok") {
		t.Fatalf("a later page is not replayed the output: %q", replay)
	}
	if err := tm.Resize(120, 40); err != nil {
		t.Fatal(err)
	}
	_ = tm.Write([]byte("exit 3\r"))
	select {
	case <-tm.Done():
	case <-time.After(15 * time.Second):
		t.Fatal("the terminal did not end")
	}
	for i := 0; i < 50; i++ {
		if exited, code := tm.State(); exited {
			if code != 3 {
				t.Fatalf("exit code %d; want 3", code)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the exit was not recorded")
}
