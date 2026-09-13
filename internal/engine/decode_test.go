package engine

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/session"
)

// decodeFile replays a captured CLI transcript through a decoder. The testdata
// files are verbatim output from the real binaries, so a format change on their
// side shows up here rather than as a blank transcript at runtime.
func decodeFile(t *testing.T, d decoder, name string) []agent.Event {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var events []agent.Event
	emit := func(e agent.Event) { events = append(events, e) }
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 8<<20)
	for sc.Scan() {
		if line := sc.Bytes(); len(line) > 0 && line[0] == '{' {
			d.line(line, emit)
		}
	}
	d.finish(emit)
	return events
}

func sessionIDs(events []agent.Event) []string {
	var out []string
	for _, e := range events {
		if s, ok := e.(agent.EvSession); ok {
			out = append(out, s.ExternalID)
		}
	}
	return out
}

func textDeltas(events []agent.Event) string {
	var sb strings.Builder
	for _, e := range events {
		if d, ok := e.(agent.EvTextDelta); ok {
			sb.WriteString(d.Text)
		}
	}
	return sb.String()
}

func assistants(events []agent.Event) []session.Message {
	var out []session.Message
	for _, e := range events {
		if a, ok := e.(agent.EvAssistant); ok {
			out = append(out, a.Message)
		}
	}
	return out
}

func toolDone(events []agent.Event) []session.ToolCall {
	var out []session.ToolCall
	for _, e := range events {
		if d, ok := e.(agent.EvToolDone); ok {
			out = append(out, d.Call)
		}
	}
	return out
}

func usage(events []agent.Event) (in, out, cache int64) {
	for _, e := range events {
		if u, ok := e.(agent.EvUsage); ok {
			in += u.In
			out += u.Out
			cache += u.CacheRead
		}
	}
	return
}

// orderOK checks the invariant the UI depends on: a tool call is committed to
// the transcript (inside an assistant message) before its result arrives, or
// the result has nothing to attach to and the call never renders.
func orderOK(t *testing.T, events []agent.Event) {
	t.Helper()
	known := map[string]bool{}
	for _, e := range events {
		switch v := e.(type) {
		case agent.EvAssistant:
			for _, c := range v.Message.Tools {
				known[c.ID] = true
			}
		case agent.EvToolDone:
			if !known[v.Call.ID] {
				t.Errorf("tool result for %q arrived before the call was committed", v.Call.ID)
			}
		}
	}
}

func TestClaudeDecoder(t *testing.T) {
	events := decodeFile(t, &claudeDec{}, "claude.jsonl")
	orderOK(t, events)

	if got := sessionIDs(events); len(got) != 1 || got[0] != "09e90558-0889-4a91-b5eb-f0603246b05b" {
		t.Errorf("session ids = %v, want exactly one init id", got)
	}
	if got := textDeltas(events); got != "Listing files." {
		t.Errorf("streamed text = %q, want %q", got, "Listing files.")
	}

	msgs := assistants(events)
	if len(msgs) != 2 {
		t.Fatalf("committed %d assistant turns, want 2: %+v", len(msgs), msgs)
	}
	if msgs[0].Text != "Listing files." || len(msgs[0].Tools) != 1 {
		t.Errorf("first turn = %+v", msgs[0])
	}
	if c := msgs[0].Tools[0]; c.Name != "Bash" || c.ID != "toolu_01" {
		t.Errorf("tool call = %+v", c)
	}
	var in map[string]any
	if json.Unmarshal(msgs[0].Tools[0].Input, &in) != nil || in["command"] != "ls" {
		t.Errorf("tool input did not survive: %s", msgs[0].Tools[0].Input)
	}
	if msgs[1].Text != "There is one file." {
		t.Errorf("second turn = %q", msgs[1].Text)
	}

	done := toolDone(events)
	if len(done) != 1 || done[0].Result != "main.go" || done[0].IsError {
		t.Errorf("tool results = %+v", done)
	}
	if i, o, c := usage(events); i != 2 || o != 5 || c != 10596 {
		t.Errorf("usage = %d/%d/%d, want 2/5/10596", i, o, c)
	}
}

func TestCodexDecoder(t *testing.T) {
	events := decodeFile(t, &codexDec{}, "codex.jsonl")
	orderOK(t, events)

	if got := sessionIDs(events); len(got) != 1 || got[0] != "01a098ec-9c69-7421-95d1-dd08580b1383" {
		t.Errorf("thread ids = %v", got)
	}

	msgs := assistants(events)
	if len(msgs) != 2 {
		t.Fatalf("committed %d turns, want 2: %+v", len(msgs), msgs)
	}
	// The text that introduces a command must ride along with it, not become
	// an orphan block above it.
	if !strings.Contains(msgs[0].Text, "run `ls`") || len(msgs[0].Tools) != 1 {
		t.Errorf("first turn = %+v", msgs[0])
	}
	if c := msgs[0].Tools[0]; c.Name != "bash" {
		t.Errorf("command_execution should map to bash, got %q", c.Name)
	}
	if msgs[1].Text != "DONE" {
		t.Errorf("final turn = %q", msgs[1].Text)
	}

	done := toolDone(events)
	if len(done) != 1 || !strings.Contains(done[0].Result, "main.go") || done[0].IsError {
		t.Errorf("tool results = %+v", done)
	}
	if i, o, c := usage(events); i != 30369 || o != 47 || c != 27136 {
		t.Errorf("usage = %d/%d/%d", i, o, c)
	}
}

func TestCodexReportsFailedCommands(t *testing.T) {
	d := &codexDec{}
	var events []agent.Event
	emit := func(e agent.Event) { events = append(events, e) }
	d.line([]byte(`{"type":"item.started","item":{"id":"i1","type":"command_execution","command":"false","status":"in_progress"}}`), emit)
	d.line([]byte(`{"type":"item.completed","item":{"id":"i1","type":"command_execution","command":"false","aggregated_output":"boom","exit_code":1,"status":"completed"}}`), emit)

	done := toolDone(events)
	if len(done) != 1 || !done[0].IsError {
		t.Errorf("a non-zero exit must be reported as an error: %+v", done)
	}
}

func TestOpenCodeDecoder(t *testing.T) {
	events := decodeFile(t, &opencodeDec{seen: map[string]bool{}}, "opencode.jsonl")
	orderOK(t, events)

	if got := sessionIDs(events); len(got) != 1 || got[0] != "ses_f6712551cffe9J370SCH7l9Xnc" {
		t.Errorf("session ids = %v", got)
	}
	msgs := assistants(events)
	if len(msgs) != 2 {
		t.Fatalf("committed %d turns, want 2: %+v", len(msgs), msgs)
	}
	if len(msgs[0].Tools) != 1 || msgs[0].Tools[0].Name != "bash" {
		t.Errorf("tool call = %+v", msgs[0].Tools)
	}
	if msgs[1].Text != "DONE" {
		t.Errorf("final turn = %q", msgs[1].Text)
	}
	if got := textDeltas(events); got != "DONE" {
		t.Errorf("streamed text = %q", got)
	}
	done := toolDone(events)
	if len(done) != 1 || !strings.Contains(done[0].Result, "main.go") {
		t.Errorf("tool results = %+v", done)
	}
}

func TestOpenCodeStreamsGrowingText(t *testing.T) {
	// opencode reports the whole text for a part each time it grows, so only
	// the new suffix may be forwarded or the transcript doubles up.
	d := &opencodeDec{seen: map[string]bool{}}
	var events []agent.Event
	emit := func(e agent.Event) { events = append(events, e) }
	for _, txt := range []string{"He", "Hello", "Hello there"} {
		d.line([]byte(`{"type":"text","sessionID":"s","part":{"id":"p1","type":"text","text":"`+txt+`"}}`), emit)
	}
	d.line([]byte(`{"type":"step_finish","sessionID":"s","part":{"reason":"stop","tokens":{"input":1,"output":2,"cache":{"read":3}}}}`), emit)

	if got := textDeltas(events); got != "Hello there" {
		t.Errorf("deltas concatenated to %q, want %q", got, "Hello there")
	}
	msgs := assistants(events)
	if len(msgs) != 1 || msgs[0].Text != "Hello there" {
		t.Errorf("committed %+v", msgs)
	}
}

func TestDecodersIgnoreGarbage(t *testing.T) {
	// Banners, progress noise and truncated lines must not panic a decoder.
	for name, d := range map[string]decoder{
		"claude":   &claudeDec{},
		"codex":    &codexDec{},
		"opencode": &opencodeDec{seen: map[string]bool{}},
	} {
		t.Run(name, func(t *testing.T) {
			emit := func(agent.Event) {}
			for _, line := range []string{`{`, `{"type":}`, `{"type":"nope"}`, `{}`, `{"type":"result"`} {
				d.line([]byte(line), emit)
			}
			d.finish(emit)
			if d.failure() != "" {
				t.Errorf("garbage should not be reported as a CLI failure: %q", d.failure())
			}
		})
	}
}

func TestClaudeErrorResultSurfaces(t *testing.T) {
	d := &claudeDec{}
	d.line([]byte(`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"context limit"}`),
		func(agent.Event) {})
	if d.failure() != "context limit" {
		t.Errorf("failure = %q, want the result text", d.failure())
	}
}

// resumeOnlyFlags are rejected by `codex exec resume`. Passing them made every
// turn after the first fail with "unexpected argument '-C' found".
var codexResumeRejects = []string{"-C", "--cd", "--sandbox"}

func TestCodexArgvMatchesTheSubcommand(t *testing.T) {
	c := newCodex("/project")

	first := codexArgv(c, agent.Turn{Prompt: "hi", Root: "/project", Mode: agent.ModeAuto}, nil)
	if !hasFlag(first, "-C") || !hasFlag(first, "--sandbox") {
		t.Errorf("a fresh run should set the directory and sandbox: %v", first)
	}
	if hasFlag(first, "resume") {
		t.Errorf("a fresh run must not resume: %v", first)
	}

	again := codexArgv(c, agent.Turn{Prompt: "hi", Root: "/project", ExternalID: "thread-1", Mode: agent.ModeAuto}, nil)
	if !hasFlag(again, "resume") || !hasFlag(again, "thread-1") {
		t.Errorf("a continued run should resume its thread: %v", again)
	}
	for _, bad := range codexResumeRejects {
		if hasFlag(again, bad) {
			t.Errorf("`codex exec resume` rejects %s, but it was passed: %v", bad, again)
		}
	}

	// Bypass is accepted by both subcommands, so it must survive a resume.
	res := codexArgv(newCodex("/project"), agent.Turn{Prompt: "hi", ExternalID: "t", Mode: agent.ModeFull}, nil)
	if !hasFlag(res, "--dangerously-bypass-approvals-and-sandbox") {
		t.Errorf("bypass was dropped on resume: %v", res)
	}
	if hasFlag(res, "--sandbox") {
		t.Errorf("--sandbox must not accompany bypass: %v", res)
	}
}

func TestPromptIsAlwaysLast(t *testing.T) {
	// A prompt that lands anywhere but the end would be read as a flag value.
	for _, tc := range []struct {
		name string
		argv []string
	}{
		{"codex fresh", codexArgv(newCodex("/p"), agent.Turn{Prompt: "do it", Mode: agent.ModeAuto}, nil)},
		{"codex resume", codexArgv(newCodex("/p"), agent.Turn{Prompt: "do it", ExternalID: "t", Mode: agent.ModeAuto}, nil)},
		{"claude fresh", claudeArgv(newClaude("/p"), agent.Turn{Prompt: "do it", Mode: agent.ModeAuto}, nil)},
		{"claude resume", claudeArgv(newClaude("/p"), agent.Turn{Prompt: "do it", ExternalID: "s", Mode: agent.ModeAuto}, nil)},
		{"opencode fresh", opencodeArgv(newOpenCode("/p"), agent.Turn{Prompt: "do it", Mode: agent.ModeAuto}, nil)},
		{"opencode resume", opencodeArgv(newOpenCode("/p"), agent.Turn{Prompt: "do it", ExternalID: "s", Mode: agent.ModeAuto}, nil)},
	} {
		if got := tc.argv[len(tc.argv)-1]; got != "do it" {
			t.Errorf("%s: last argument is %q, want the prompt: %v", tc.name, got, tc.argv)
		}
	}
}

func hasFlag(argv []string, flag string) bool {
	for _, a := range argv {
		if a == flag {
			return true
		}
	}
	return false
}

// TestForkArgv checks each CLI's own branch mechanism is used, so a forked
// session keeps the context the agent already built rather than replaying it.
func TestForkArgv(t *testing.T) {
	turn := agent.Turn{Prompt: "go on", Root: "/p", ExternalID: "sess-1", Fork: true, Mode: agent.ModeAuto}

	cl := claudeArgv(newClaude("/p"), turn, nil)
	if !hasFlag(cl, "--resume") || !hasFlag(cl, "--fork-session") {
		t.Errorf("claude should resume and fork: %v", cl)
	}

	cx := codexArgv(newCodex("/p"), turn, nil)
	if !hasFlag(cx, "fork") || hasFlag(cx, "resume") {
		t.Errorf("codex should use its fork subcommand: %v", cx)
	}
	// fork rejects the same flags resume does.
	for _, bad := range codexResumeRejects {
		if hasFlag(cx, bad) {
			t.Errorf("`codex exec fork` rejects %s, but it was passed: %v", bad, cx)
		}
	}

	oc := opencodeArgv(newOpenCode("/p"), turn, nil)
	if !hasFlag(oc, "-s") || !hasFlag(oc, "--fork") {
		t.Errorf("opencode should fork its session: %v", oc)
	}
}

// TestForkWithoutAnIDStartsFresh: there is nothing to branch from, so the fork
// flag must not leak into the command line.
func TestForkWithoutAnIDStartsFresh(t *testing.T) {
	turn := agent.Turn{Prompt: "hi", Root: "/p", Fork: true, Mode: agent.ModeAuto}

	for name, argv := range map[string][]string{
		"claude":   claudeArgv(newClaude("/p"), turn, nil),
		"codex":    codexArgv(newCodex("/p"), turn, nil),
		"opencode": opencodeArgv(newOpenCode("/p"), turn, nil),
	} {
		for _, bad := range []string{"--fork-session", "--fork", "fork", "resume", "--resume", "-s"} {
			if hasFlag(argv, bad) {
				t.Errorf("%s passed %s with no session to branch from: %v", name, bad, argv)
			}
		}
	}
}
