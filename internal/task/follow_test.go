package task

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fastFollow(t *testing.T) {
	t.Helper()
	old := followEvery
	followEvery = 10 * time.Millisecond
	t.Cleanup(func() { followEvery = old })
}

// eventually waits for the task's output to satisfy ok.
func eventually(t *testing.T, tk *Task, what string, ok func(string) bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ok(strings.Join(tk.Output(), "\n")) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s; output is %q", what, strings.Join(tk.Output(), "\n"))
}

// TestFollowShowsOutputAsItIsWritten is the point: a command another program
// runs can be watched while it runs, not only read once it is over.
func TestFollowShowsOutputAsItIsWritten(t *testing.T) {
	fastFollow(t)
	path := filepath.Join(t.TempDir(), "b1.output")
	r := NewRegistry()
	tk := r.Adopt("b1", "slow build", "s1")
	r.Follow("b1", []string{path})

	// The file does not exist yet when the command is announced.
	time.Sleep(30 * time.Millisecond)
	if err := os.WriteFile(path, []byte("step 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eventually(t, tk, "the first line never showed", func(s string) bool { return s == "step 1" })

	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("step 2\n")
	f.Close()
	eventually(t, tk, "a later line never showed", func(s string) bool { return s == "step 1\nstep 2" })

	r.Update("b1", Done, "output: "+path)
	eventually(t, tk, "the note was lost to the file", func(s string) bool {
		return strings.HasSuffix(s, "output: "+path) && strings.HasPrefix(s, "step 1")
	})
}

// TestFollowKeepsWhatItReadWhenTheFileGoes: a foreground command's file is
// removed when it ends, and the output must not vanish with it.
func TestFollowKeepsWhatItReadWhenTheFileGoes(t *testing.T) {
	fastFollow(t)
	path := filepath.Join(t.TempDir(), "f1.output")
	os.WriteFile(path, []byte("fg 1\nfg 2\n"), 0o644)

	r := NewRegistry()
	tk := r.Adopt("f1", "loop", "s1")
	r.Follow("f1", []string{path})
	eventually(t, tk, "never read", func(s string) bool { return s == "fg 1\nfg 2" })

	os.Remove(path)
	r.Update("f1", Done, "")
	time.Sleep(50 * time.Millisecond)
	if got := strings.Join(tk.Output(), "\n"); got != "fg 1\nfg 2" {
		t.Errorf("output after the file went: %q", got)
	}
}

func TestFollowFindsTheFileByPattern(t *testing.T) {
	fastFollow(t)
	dir := t.TempDir()
	real := filepath.Join(dir, "spelled-otherwise", "sess", "tasks", "b2.output")
	os.MkdirAll(filepath.Dir(real), 0o755)
	os.WriteFile(real, []byte("found\n"), 0o644)

	r := NewRegistry()
	tk := r.Adopt("b2", "x", "s1")
	r.Follow("b2", []string{
		filepath.Join(dir, "exact-guess", "sess", "tasks", "b2.output"),
		filepath.Join(dir, "*", "sess", "tasks", "b2.output"),
	})
	eventually(t, tk, "the pattern did not find it", func(s string) bool { return s == "found" })
	r.Update("b2", Done, "")
}

func TestSplitOutputShowsTheLastFrameOfAProgressBar(t *testing.T) {
	got := splitOutput("downloading\r 10%\r 55%\r100%\r\ndone\r\n")
	want := []string{"100%", "done"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q, want %q", got, want)
	}
	if splitOutput("") != nil || splitOutput("\n") != nil {
		t.Error("nothing written should be no lines")
	}
}

func TestReadTailStartsOnALineBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.output")
	os.WriteFile(path, []byte("aaaaaaaaaa\nbbbb\ncccc\n"), 0o644)
	lines, ok := readTail(path, 12) // lands inside the first line
	if !ok || strings.Join(lines, "|") != "bbbb|cccc" {
		t.Errorf("got %q ok=%v", lines, ok)
	}
}
