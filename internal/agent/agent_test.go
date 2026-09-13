package agent

import (
	"encoding/json"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/phanngoc/agent-tui/internal/session"
)

func TestReplayRebuildsApiHistory(t *testing.T) {
	msgs := []session.Message{
		{Role: session.RoleUser, Text: "find the ranking code"},
		{Role: session.RoleAssistant, Text: "Looking now.", Tools: []session.ToolCall{{
			ID: "tu_1", Name: "grep", Input: json.RawMessage(`{"query":"rank"}`),
			Result: "search/rank.go:12: func score()", Done: true,
		}}},
		{Role: session.RoleAssistant, Text: "It's in search/rank.go."},
	}

	got := Replay(msgs)

	// user, assistant(+tool_use), user(tool_result), assistant
	if len(got) != 4 {
		t.Fatalf("replay produced %d messages, want 4", len(got))
	}
	roles := []anthropic.MessageParamRole{
		anthropic.MessageParamRoleUser,
		anthropic.MessageParamRoleAssistant,
		anthropic.MessageParamRoleUser,
		anthropic.MessageParamRoleAssistant,
	}
	for i, want := range roles {
		if got[i].Role != want {
			t.Errorf("message %d role = %q, want %q", i, got[i].Role, want)
		}
	}
	if len(got[1].Content) != 2 {
		t.Fatalf("assistant turn has %d blocks, want text + tool_use", len(got[1].Content))
	}
	if got[1].Content[1].OfToolUse == nil || got[1].Content[1].OfToolUse.ID != "tu_1" {
		t.Errorf("tool_use block missing or wrong: %+v", got[1].Content[1])
	}
	if got[2].Content[0].OfToolResult == nil || got[2].Content[0].OfToolResult.ToolUseID != "tu_1" {
		t.Errorf("tool_result does not pair with the call: %+v", got[2].Content[0])
	}
}

func TestReplaySkipsEmptyTurns(t *testing.T) {
	// An interrupted turn can leave an assistant message with neither text nor
	// tool calls; sending that to the API would be rejected.
	got := Replay([]session.Message{
		{Role: session.RoleUser, Text: "hi"},
		{Role: session.RoleAssistant},
		{Role: session.RoleUser, Text: "   "},
	})
	if len(got) != 1 {
		t.Fatalf("replay kept %d messages, want only the first: %+v", len(got), got)
	}
}

func TestReplaySubstitutesMissingToolResults(t *testing.T) {
	// A turn cancelled mid-tool leaves a call with no result. The API still
	// requires a tool_result for every tool_use, so one must be synthesised.
	got := Replay([]session.Message{
		{Role: session.RoleAssistant, Tools: []session.ToolCall{{ID: "x", Name: "bash"}}},
	})
	if len(got) != 2 {
		t.Fatalf("want an assistant turn plus its tool_result, got %d", len(got))
	}
	res := got[1].Content[0].OfToolResult
	if res == nil || len(res.Content) == 0 || res.Content[0].OfText.Text == "" {
		t.Errorf("missing placeholder tool_result: %+v", got[1].Content[0])
	}
}

func TestPrepareCachesLiveHistory(t *testing.T) {
	a := New("k", &Executor{}, "claude-opus-5", "high", 1000)
	s := &session.Session{Messages: []session.Message{{Role: session.RoleUser, Text: "hi"}}}

	first := a.Prepare(s)
	if len(first) != 1 {
		t.Fatalf("Prepare returned %d messages", len(first))
	}
	// A second call must reuse what is on the session, not rebuild it, so that
	// thinking blocks captured during the run survive.
	s.Live = append(first, anthropic.NewAssistantMessage(anthropic.NewTextBlock("ok")))
	if got := a.Prepare(s); len(got) != 2 {
		t.Errorf("Prepare rebuilt history instead of reusing it: %d messages", len(got))
	}
}

func TestEffortMapping(t *testing.T) {
	for in, want := range map[string]anthropic.OutputConfigEffort{
		"low":     anthropic.OutputConfigEffortLow,
		"medium":  anthropic.OutputConfigEffortMedium,
		"high":    anthropic.OutputConfigEffortHigh,
		"xhigh":   anthropic.OutputConfigEffortXhigh,
		"max":     anthropic.OutputConfigEffortMax,
		"garbage": anthropic.OutputConfigEffortHigh,
	} {
		if got := New("k", &Executor{}, "m", in, 1).Effort; got != want {
			t.Errorf("effort %q mapped to %q, want %q", in, got, want)
		}
	}
}
