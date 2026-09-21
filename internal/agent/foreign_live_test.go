package agent

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/phanngoc/agent-tui/internal/session"
)

// Does the API accept a tool call it never offered?
//
// Replay rebuilds SDK history from the persisted transcript, and a transcript
// written by an external CLI names its tools the way that CLI does: Claude
// Code writes Edit, Write and Bash where the built-in executor declares
// edit_file, write_file and bash. Hand such a session to the built-in engine
// and the request carries a tool_use block for a tool that is not in Tools.
//
// This already happens on every CLI → api switch, and handing a session over
// deliberately makes it routine — so it was worth asking rather than assuming.
//
// Asked on 2026-09-21: the API accepts it. A tool_use in the history whose
// name is absent from Tools is fine; the turn came back stop_reason=tool_use,
// so the model read the foreign call, understood it, and went on to use one of
// the tools it actually had. No flattening is needed, and Replay stays as it
// is: a transcript is a record of what happened, and rewriting one to look
// like it happened elsewhere would be a lie told to the next engine.
//
// The test stays because the answer could change. If it ever does, the fix is
// to flatten a foreign call into text inside Replay — never to invent a
// definition for it, because a tool the executor cannot run is worse offered
// than absent.
//
// Costs a real call, so it needs AGENT_TUI_LIVE=1 and a credential.
func TestLiveAPIAcceptsAForeignToolName(t *testing.T) {
	if os.Getenv("AGENT_TUI_LIVE") != "1" {
		t.Skip("set AGENT_TUI_LIVE=1 and a credential to ask the API")
	}
	if os.Getenv("ANTHROPIC_API_KEY") == "" && os.Getenv("ANTHROPIC_AUTH_TOKEN") == "" {
		t.Skip("no credential: set ANTHROPIC_API_KEY")
	}

	// A transcript as Claude Code leaves it behind: its own spellings, its own
	// argument names.
	in, err := json.Marshal(map[string]string{
		"file_path":  "main.go",
		"old_string": "hello",
		"new_string": "xin chào",
	})
	if err != nil {
		t.Fatal(err)
	}
	transcript := []session.Message{
		{Role: session.RoleUser, Text: "đổi lời chào trong main.go"},
		{Role: session.RoleAssistant, Text: "Sửa rồi.", Tools: []session.ToolCall{{
			ID: "toolu_foreign_1", Name: "Edit", Input: in,
			Result: "The file main.go has been updated.", Done: true,
		}}},
		{Role: session.RoleUser, Text: "nó ở dòng nào?"},
	}

	exec := &Executor{Root: t.TempDir(), MaxBytes: 1 << 20, Workers: 1}
	params := anthropic.MessageNewParams{
		// The cheapest model there is. The question is whether the request is
		// well-formed, and validation happens before any model sees it.
		Model:     anthropic.Model("claude-haiku-4-5"),
		MaxTokens: 256,
		System: []anthropic.TextBlockParam{{
			Text: "Answer in one short sentence.",
		}},
		// The built-in tool set, which has never heard of Edit.
		Tools:    exec.Defs(ModeAuto),
		Messages: Replay(transcript),
	}
	assertNoToolNamed(t, params, "Edit")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	client := anthropic.NewClient()
	msg, err := client.Messages.New(ctx, params)
	if err != nil {
		// The answer this test exists to get. It is a finding, not a flake:
		// fail loudly with the API's own words so the fix is written against
		// what it actually said.
		t.Fatalf("the API refused a history containing a tool_use it never offered.\n"+
			"Replay must flatten foreign tool calls into text.\n\nAPI said: %v", err)
	}
	if msg == nil || len(msg.Content) == 0 {
		t.Fatal("the API accepted the request and said nothing")
	}
	t.Logf("accepted: a historical tool_use named Edit, absent from Tools, is fine.\n"+
		"stop_reason=%s in=%d out=%d", msg.StopReason, msg.Usage.InputTokens, msg.Usage.OutputTokens)
}

// assertNoToolNamed makes sure the experiment is the one described: the
// history names a tool, and the request does not declare it. Without this the
// test could pass by accidentally offering the very thing it is asking about.
func assertNoToolNamed(t *testing.T, p anthropic.MessageNewParams, name string) {
	t.Helper()
	for _, tl := range p.Tools {
		if tl.OfTool != nil && tl.OfTool.Name == name {
			t.Fatalf("the request declares %q, so it does not test anything", name)
		}
	}
	var found bool
	for _, m := range p.Messages {
		for _, b := range m.Content {
			if b.OfToolUse != nil && b.OfToolUse.Name == name {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("Replay did not put a tool_use named %q in the history; "+
			"either it changed, or the fixture no longer represents a CLI transcript", name)
	}
}

// And the same question asked of the transcript alone, which needs no network:
// Replay does pass a foreign name through untouched. If that ever stops being
// true the live test above is measuring nothing.
func TestReplayPassesForeignToolNamesThrough(t *testing.T) {
	in, _ := json.Marshal(map[string]string{"file_path": "main.go"})
	out := Replay([]session.Message{
		{Role: session.RoleUser, Text: "sửa đi"},
		{Role: session.RoleAssistant, Text: "rồi", Tools: []session.ToolCall{
			{ID: "t1", Name: "Edit", Input: in, Result: "done", Done: true},
		}},
	})

	var names []string
	for _, m := range out {
		for _, b := range m.Content {
			if b.OfToolUse != nil {
				names = append(names, b.OfToolUse.Name)
			}
		}
	}
	if got := strings.Join(names, ","); got != "Edit" {
		t.Errorf("Replay emitted tool names %q, want the CLI's own spelling", got)
	}
}
