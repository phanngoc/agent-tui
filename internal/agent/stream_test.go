package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/shell"
)

// The point of forwarding a call while it is written is that the summary can be
// read before the turn ends. Anything less than that is just an earlier blank
// line.
func TestPendingCallsShowThePathAsItArrives(t *testing.T) {
	var p pendingCalls
	p.start(0, "tool_1", "read_file")

	if got := p.calls()[0].Summary(); got != "" {
		t.Errorf("a call with no input yet summarised as %q", got)
	}
	for _, fragment := range []string{`{"pa`, `th": "internal/`, `ui/chat.go"}`} {
		if !p.grow(0, fragment) {
			t.Fatal("a fragment was dropped")
		}
	}
	calls := p.calls()
	if len(calls) != 1 || calls[0].ID != "tool_1" || calls[0].Name != "read_file" {
		t.Fatalf("got %+v", calls)
	}
	if got := calls[0].Summary(); got != "internal/ui/chat.go" {
		t.Errorf("Summary() = %q", got)
	}
}

// Calls keep the order the model wrote them in, and a fragment for a block
// nobody announced changes nothing rather than inventing one.
func TestPendingCallsKeepOrder(t *testing.T) {
	var p pendingCalls
	p.start(0, "a", "read_file")
	p.start(1, "b", "grep")
	p.grow(0, `{"path":"x.go"}`)
	p.grow(1, `{"query":"Token"}`)

	if p.grow(7, `{"nope":1}`) {
		t.Error("a fragment for an unknown block was accepted")
	}
	calls := p.calls()
	if len(calls) != 2 || calls[0].ID != "a" || calls[1].ID != "b" {
		t.Fatalf("got %+v", calls)
	}
}

// liveOutput hands over whole lines and keeps everything for the model. Half a
// line is held back: a progress bar redrawing itself would otherwise arrive as
// a thousand redraws of the pane.
func TestLiveOutputForwardsWholeLines(t *testing.T) {
	var got []string
	w := &liveOutput{emit: func(s string) { got = append(got, s) }, max: 1 << 20}

	w.Write([]byte("one\ntw"))
	if len(got) != 1 || got[0] != "one\n" {
		t.Fatalf("after a line and a half: %q", got)
	}
	// Too soon for another flush, so this waits with the rest of its line.
	w.Write([]byte("o\n"))
	w.Close()

	if strings.Join(got, "") != "one\ntwo\n" {
		t.Errorf("forwarded %q", got)
	}
	if w.String() != "one\ntwo\n" {
		t.Errorf("kept %q for the model", w.String())
	}
}

// A carriage return is a terminal redrawing a line in place. Keeping it would
// put the cursor back at the start of a transcript row that is not a terminal.
func TestLiveOutputDropsCarriageReturns(t *testing.T) {
	var got []string
	w := &liveOutput{emit: func(s string) { got = append(got, s) }, max: 1 << 20}
	w.Write([]byte("50%\r100%\ndone\n"))
	w.Close()
	if joined := strings.Join(got, ""); joined != "50%100%\ndone\n" {
		t.Errorf("forwarded %q", joined)
	}
}

// What the model is given stays capped, however much the command printed.
func TestLiveOutputCapsWhatItKeeps(t *testing.T) {
	w := &liveOutput{max: 32}
	for range 100 {
		w.Write([]byte("0123456789\n"))
	}
	w.Close()
	if len(w.String()) > 64 {
		t.Errorf("kept %d bytes for a 32 byte cap", len(w.String()))
	}
}

// End to end: a command's output reaches the watcher before the command is
// over, which is the whole point.
func TestBashStreamsWhileItRuns(t *testing.T) {
	if !shell.POSIX(true) {
		t.Skip("no POSIX shell to run a portable script in")
	}
	e := newExec(t, nil)

	start := time.Now()
	text, isErr, chunks := runLive(t, e, "bash", map[string]any{
		"command": "echo first; sleep 0.4; echo second",
	})
	if isErr {
		t.Fatalf("command failed: %s", text)
	}
	if time.Since(start) < 300*time.Millisecond {
		t.Skip("the shell did not actually sleep; nothing to interleave")
	}
	if len(chunks) < 2 {
		t.Errorf("output arrived in %d piece(s), want the first line before the second",
			len(chunks))
	}
	if !strings.Contains(chunks[0], "first") {
		t.Errorf("the first chunk was %q", chunks[0])
	}
	// The model still sees all of it, in order, exactly once.
	if !strings.Contains(text, "first") || !strings.Contains(text, "second") {
		t.Errorf("the result handed to the model was %q", text)
	}
	if strings.Count(text, "first") != 1 {
		t.Errorf("the result repeats itself: %q", text)
	}
}

// The outcome line is read off the result, so the wording of a tool's result is
// part of its contract. This is where a rewording is caught.
func TestEveryToolResultProducesAnOutcome(t *testing.T) {
	e := newExec(t, map[string]string{
		"a.go":     "package a\nconst Token = 1\n",
		"sub/b.go": "// Token here\n",
	})
	for _, tc := range []struct {
		name string
		in   any
		want string
	}{
		{"read_file", map[string]any{"path": "a.go"}, "lines 1-2 of 2"},
		{"list_dir", map[string]any{"path": "."}, "2 entries"},
		{"grep", map[string]any{"query": "Token"}, "2 match(es) in 2 file(s)"},
		{"find_files", map[string]any{"query": "a.go"}, "1 match(es) for \"a.go\""},
		{"write_file", map[string]any{"path": "c.go", "content": "package c\n"},
			"created (10 bytes, 2 lines)"},
		{"edit_file", map[string]any{"path": "a.go", "old_string": "const", "new_string": "var"},
			"edited (1 replacement(s))"},
	} {
		text, isErr := run(t, e, tc.name, tc.in)
		if isErr {
			t.Fatalf("%s failed: %s", tc.name, text)
		}
		raw, _ := json.Marshal(tc.in)
		call := session.ToolCall{
			Name: tc.name, Input: raw, Result: text, Done: true,
		}
		if got := call.Outcome(); got != tc.want {
			t.Errorf("%s: Outcome() = %q, want %q\nresult was:\n%s", tc.name, got, tc.want, text)
		}
	}
}
