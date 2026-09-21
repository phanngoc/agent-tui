package session

import (
	"encoding/json"
	"strings"
	"testing"
)

// call builds a finished tool call with a result of the given size.
func call(name, path, result string) ToolCall {
	in, _ := json.Marshal(map[string]string{"file_path": path})
	return ToolCall{ID: "t", Name: name, Input: in, Result: result, Done: true}
}

func say(role, text string, tools ...ToolCall) Message {
	return Message{Role: role, Text: text, Tools: tools}
}

// The whole reason a brief is not just the transcript: tool payloads are
// eleven times the size of the conversation around them, and none of them is
// what the next engine needs to know.
func TestBriefNeverInlinesToolJSON(t *testing.T) {
	bigIn := strings.Repeat("x", 4000)
	bigOut := strings.Repeat("y", 60000)
	in, _ := json.Marshal(map[string]string{"file_path": "internal/ui/chat.go", "old_string": bigIn})

	out := Brief([]Message{
		say("user", "sửa chat.go"),
		say("assistant", "Xong.", ToolCall{
			ID: "t1", Name: "edit_file", Input: in, Result: bigOut, Done: true,
		}),
	}, BriefLimit)

	if strings.Contains(out, bigIn) {
		t.Error("the tool's input JSON was inlined")
	}
	if strings.Contains(out, bigOut) {
		t.Error("the tool's result was inlined")
	}
	if !strings.Contains(out, "internal/ui/chat.go") {
		t.Errorf("the call lost the one thing worth keeping:\n%s", out)
	}
	if len(out) > BriefLimit {
		t.Errorf("the brief is %d bytes against a limit of %d", len(out), BriefLimit)
	}
}

// A brief that runs past the limit does not degrade — the process fails to
// start, with an error about nothing the reader did. So the worst realistic
// transcript has to fit, not merely the average one.
func TestBriefFitsTheLimit(t *testing.T) {
	// Modelled on the largest session on disk: 163 messages, 144 tool calls,
	// forty thousand characters of prose.
	var msgs []Message
	for i := 0; i < 163; i++ {
		role := RoleUser
		if i%2 == 1 {
			role = RoleAssistant
		}
		m := say(role, strings.Repeat("một câu dài về resolvePfid. ", 20))
		if role == RoleAssistant {
			for j := 0; j < 3; j++ {
				m.Tools = append(m.Tools,
					call("read_file", "internal/ui/chat.go", strings.Repeat("z", 3000)))
			}
		}
		msgs = append(msgs, m)
	}

	out := Brief(msgs, BriefLimit)
	if len(out) > BriefLimit {
		t.Errorf("the brief is %d bytes against a limit of %d", len(out), BriefLimit)
	}
	if !strings.Contains(out, briefClose) {
		t.Error("the brief was cut off before its closing tag")
	}
}

// Silently lossy is the one thing a briefing may not be.
func TestBriefSaysHowMuchItLeftOut(t *testing.T) {
	var msgs []Message
	for i := 0; i < 200; i++ {
		msgs = append(msgs, say(RoleUser, strings.Repeat("chuyện cũ. ", 30)))
	}
	msgs = append(msgs, say(RoleAssistant, "ĐIỀU CUỐI CÙNG"))

	out := Brief(msgs, BriefLimit)
	if !strings.Contains(out, "left out") {
		t.Errorf("the brief dropped messages without saying so:\n%s", out)
	}
	if !strings.Contains(out, "ĐIỀU CUỐI CÙNG") {
		t.Error("the end was cut instead of the beginning")
	}
	if strings.Contains(out, "chuyện cũ. chuyện cũ. chuyện cũ. chuyện cũ. chuyện cũ. chuyện cũ.") &&
		strings.Index(out, "ĐIỀU CUỐI CÙNG") < strings.Index(out, "left out") {
		t.Error("the elision line is in the wrong place")
	}
}

// It travels in the same string as the user's question, so the boundary has to
// be impossible to misread.
func TestBriefReadsAsARecordNotARequest(t *testing.T) {
	out := Brief([]Message{say(RoleUser, "làm X"), say(RoleAssistant, "rồi")}, BriefLimit)

	for _, want := range []string{"<handoff>", "</handoff>", "already happened", "Do not\nredo it"} {
		if !strings.Contains(out, want) {
			t.Errorf("the brief does not say %q:\n%s", want, out)
		}
	}
	if !strings.HasSuffix(out, briefClose) {
		t.Error("the brief does not end where it says the user's message begins")
	}
}

// Every engine spells its tools differently, and the brief has to read the
// same whoever wrote the conversation.
func TestBriefSpeaksEveryEngineSpelling(t *testing.T) {
	for _, name := range []string{"Edit", "edit_file", "edit", "Write", "write_file"} {
		out := Brief([]Message{
			say(RoleAssistant, "đổi rồi", call(name, "internal/ui/view.go", "updated 2 lines")),
		}, BriefLimit)
		if !strings.Contains(out, "internal/ui/view.go") {
			t.Errorf("%s: the path is missing:\n%s", name, out)
		}
		if !strings.Contains(out, "files touched: internal/ui/view.go") {
			t.Errorf("%s: the file it wrote is not in the footer:\n%s", name, out)
		}
	}
}

// Nothing to catch up on renders as nothing at all, so the ordinary turn pays
// no price for the feature.
func TestBriefOfNothingIsEmpty(t *testing.T) {
	if got := Brief(nil, BriefLimit); got != "" {
		t.Errorf("an empty conversation briefed as %q", got)
	}
}

// A command the user ran is part of the conversation rather than a detour from
// it, and reading as one costs a line.
func TestBriefKeepsShellRuns(t *testing.T) {
	out := Brief([]Message{{
		Role:  RoleUser,
		Text:  "I moved this session",
		Shell: &ShellRun{Command: "cd internal", Done: true},
	}}, BriefLimit)

	if !strings.Contains(out, "$ cd internal") {
		t.Errorf("the command is not in the brief:\n%s", out)
	}
}
