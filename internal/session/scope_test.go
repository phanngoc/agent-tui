package session

import (
	"encoding/json"
	"testing"
)

func TestPathsInside(t *testing.T) {
	const root = "/home/me/project"
	for _, tc := range []struct {
		name            string
		input           string
		inside, decided bool
	}{
		{"write inside", `{"file_path":"/home/me/project/a.go","content":"x"}`, true, true},
		{"write outside", `{"file_path":"/tmp/a.go","content":"x"}`, false, true},
		{"relative is inside by definition", `{"file_path":"src/a.go"}`, true, true},
		{"relative climbing out", `{"file_path":"../elsewhere/a.go"}`, false, true},
		{"the root itself", `{"path":"/home/me/project"}`, true, true},
		{"a sibling with the same prefix", `{"path":"/home/me/project-other/a.go"}`, false, true},
		{"dot segments resolved", `{"file_path":"/home/me/project/src/../a.go"}`, true, true},
		{"escaping via dot segments", `{"file_path":"/home/me/project/../../etc/passwd"}`, false, true},
		{"a shell command names no path", `{"command":"rm -rf /tmp/x"}`, false, false},
		{"no input at all", `{}`, false, false},
		{"nested edits, all inside", `{"edits":[{"file_path":"/home/me/project/a.go"},{"file_path":"/home/me/project/b.go"}]}`, true, true},
		{"nested edits, one outside", `{"edits":[{"file_path":"/home/me/project/a.go"},{"file_path":"/etc/hosts"}]}`, false, true},
	} {
		call := ToolCall{Input: json.RawMessage(tc.input)}
		inside, decided := call.PathsInside(root)
		if inside != tc.inside || decided != tc.decided {
			t.Errorf("%s: inside=%v decided=%v, want inside=%v decided=%v",
				tc.name, inside, decided, tc.inside, tc.decided)
		}
	}
}

func TestPathsInsideHandlesJunk(t *testing.T) {
	for _, in := range []string{``, `not json`, `[]`, `null`, `{"file_path":123}`} {
		call := ToolCall{Input: json.RawMessage(in)}
		if _, decided := call.PathsInside("/root"); decided {
			t.Errorf("%q should not produce a decision", in)
		}
	}
	rooted := ToolCall{Input: json.RawMessage(`{"path":"/x"}`)}
	if _, decided := rooted.PathsInside(""); decided {
		t.Error("with no root there is nothing to compare against")
	}
}
