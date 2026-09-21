package session

import (
	"encoding/json"
	"testing"
)

// A call is streamed as a run of fragments, so for most of its life its input
// is not JSON. The line still has to say which file is being written, because
// that is the whole reason to look at it while it happens.
func TestSummaryReadsACallStillArriving(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"nothing yet", `{"path"`, ""},
		{"key only", `{"path":`, ""},
		{"half a path", `{"path": "internal/ui/ch`, "internal/ui/ch"},
		{"a whole path, no close", `{"path": "internal/ui/chat.go"`, "internal/ui/chat.go"},
		{"finished", `{"path": "internal/ui/chat.go"}`, "internal/ui/chat.go"},
		// The interesting one: write_file sends the path first and then
		// thousands of bytes of content, so this is what is on screen for as
		// long as the write takes.
		{"path then content", `{"path": "a.go", "content": "package a\nfunc`, "a.go"},
	} {
		got := ToolCall{Name: "read_file", Input: json.RawMessage(tc.in)}.Summary()
		if got != tc.want {
			t.Errorf("%s: Summary() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A number or a nested object before the field that matters must not stop the
// scan at the first thing that is not a string.
func TestSummarySkipsNonStringFields(t *testing.T) {
	in := `{"limit": 30, "regex": true, "opts": {"a": 1}, "query": "renderTool`
	got := ToolCall{Name: "grep", Input: json.RawMessage(in)}.Summary()
	if got != "renderTool" {
		t.Errorf("Summary() = %q, want the query", got)
	}
}

func TestOutcomeSaysWhatCameBack(t *testing.T) {
	for _, tc := range []struct {
		name string
		call ToolCall
		want string
	}{
		{"a read", ToolCall{
			Name: "read_file", Done: true,
			Input:  json.RawMessage(`{"path":"a.go"}`),
			Result: "a.go (lines 1-40 of 249)\n     1\tpackage a\n",
		}, "lines 1-40 of 249"},

		{"a write", ToolCall{
			Name: "write_file", Done: true,
			Input:  json.RawMessage(`{"path":"a.go"}`),
			Result: "created a.go (120 bytes, 8 lines)",
		}, "created (120 bytes, 8 lines)"},

		{"a search", ToolCall{
			Name: "grep", Done: true,
			Input:  json.RawMessage(`{"query":"Token"}`),
			Result: "8 match(es) in 3 file(s):\n a.go:2\n",
		}, "8 match(es) in 3 file(s)"},

		{"a command", ToolCall{
			Name: "bash", Done: true,
			Input:  json.RawMessage(`{"command":"go build ./..."}`),
			Result: "one\ntwo\nthree\n",
		}, "3 lines"},

		{"a silent command", ToolCall{
			Name: "bash", Done: true,
			Input:  json.RawMessage(`{"command":"true"}`),
			Result: "(no output, exit 0)",
		}, "no output"},

		{"a failed command", ToolCall{
			Name: "bash", Done: true, IsError: true,
			Input:  json.RawMessage(`{"command":"false"}`),
			Result: "boom\n(exit: 1)",
		}, "failed"},

		{"a listing", ToolCall{
			Name: "list_dir", Done: true,
			Input:  json.RawMessage(`{"path":"internal"}`),
			Result: "internal\n  ui/\n  agent/\n  session/\n",
		}, "3 entries"},

		// Nothing has come back yet, and nothing is claimed.
		{"still running", ToolCall{Name: "bash", Input: json.RawMessage(`{}`)}, ""},
		{"denied", ToolCall{Name: "bash", Done: true, Denied: true, Result: "no"}, ""},
	} {
		if got := tc.call.Outcome(); got != tc.want {
			t.Errorf("%s: Outcome() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A CLI's tools are not this process's, so their results have shapes nothing
// here chose. The line must stay short rather than spill a paragraph into it.
func TestOutcomeStaysShortForAnUnknownTool(t *testing.T) {
	long := ToolCall{
		Name: "WebFetch", Done: true,
		Result: "Fetched and summarised the page at great length, " +
			"far more than belongs on a single line of a transcript pane.",
	}
	if got := long.Outcome(); got != "" {
		t.Errorf("Outcome() = %q, want nothing rather than a paragraph", got)
	}
}
