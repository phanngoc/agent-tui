package telegram

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/gateway"
	"github.com/phanngoc/agent-tui/internal/session"
)

func call(id, name, input string) session.ToolCall {
	return session.ToolCall{ID: id, Name: name, Input: json.RawMessage(input)}
}

// The progress message says what the turn is doing, step by step, in a
// form a phone shows well; when it is over it folds into what was done.
func TestProgressRendersStepsAndSummary(t *testing.T) {
	t0 := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	p := newProgress("", t0)
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	p.apply(gateway.New(gateway.EvStatus, "s", gateway.TextData{Text: "thinking"}), at(0))
	p.apply(gateway.New(gateway.EvThinkingDelta, "s", gateway.TextData{Text: "The guard rejects a second submit. It needs the result to be null first."}), at(1))
	out := p.render(at(2))
	for _, want := range []string{"⏳ <b>Thinking</b> · 2s", "🧠 <i>"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
	bash := call("t1", "Bash", `{"command":"npm test -- --run"}`)
	p.apply(gateway.New(gateway.EvMessage, "s", gateway.MessageData{Message: session.Message{Role: session.RoleAssistant, Text: "Let me run the tests first.", Tools: []session.ToolCall{bash}}}), at(3))
	p.apply(gateway.New(gateway.EvToolStart, "s", gateway.ToolData{Call: bash}), at(3))
	out = p.render(at(9))
	for _, want := range []string{"1 step", "💬 <i>Let me run the tests first.</i>", "⏳ 💻 Bash <code>npm test -- --run</code> <i>6s</i>"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "🧠") {
		t.Fatal("the old thinking is still shown while a tool runs")
	}
	done := bash
	done.Done, done.IsError = true, true
	p.apply(gateway.New(gateway.EvToolDone, "s", gateway.ToolData{Call: done}), at(10))
	for i := 0; i < 9; i++ {
		c := call("r"+string(rune('a'+i)), "Read", `{"file_path":"/repo/src/lib/file.ts"}`)
		p.apply(gateway.New(gateway.EvToolStart, "s", gateway.ToolData{Call: c}), at(11))
		c.Done = true
		p.apply(gateway.New(gateway.EvToolDone, "s", gateway.ToolData{Call: c}), at(11))
	}
	out = p.render(at(12))
	for _, want := range []string{"10 steps", "<i>… 2 earlier steps</i>", "✅ 📖 Read <code>lib/file.ts</code>"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
	sum := p.summary(at(75), "")
	for _, want := range []string{"✅ <b>Done</b> · 1m 15s · 10 steps", "📖 Read ×9 · 💻 Bash", "<i>1 step failed</i>"} {
		if !strings.Contains(sum, want) {
			t.Fatalf("summary missing %q in\n%s", want, sum)
		}
	}
	if !strings.HasPrefix(p.summary(at(5), "context canceled"), "⏹ <b>Stopped</b>") {
		t.Fatal("a stop does not read as a stop")
	}
}

func TestDescribeTool(t *testing.T) {
	for _, tc := range []struct{ name, input, icon, label string }{
		{"mcp__datadog__search_datadog_logs", `{}`, "🔌", ""},
		{"WebFetch", `{"url":"https://docs.openclaw.ai/channels/telegram?x=1"}`, "🌐", "docs.openclaw.ai/channels/telegram"},
		{"Edit", `{"file_path":"C:\\repo\\internal\\bot.go"}`, "✏️", "internal/bot.go"},
		{"Grep", `{"pattern":"push_provider","path":"/repo/src"}`, "🔎", "push_provider · repo/src"},
		{"Agent", `{"description":"Find the callers"}`, "🤖", "Find the callers"},
	} {
		icon, label := describeTool(call("x", tc.name, tc.input))
		if icon != tc.icon || label != tc.label {
			t.Errorf("%s: %s %q; want %s %q", tc.name, icon, label, tc.icon, tc.label)
		}
	}
	if toolName("mcp__datadog__search_logs") != "datadog · search_logs" {
		t.Error("toolName")
	}
}

func lastSent(tg *fakeTelegram, method string) map[string]any {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	for i := len(tg.sent) - 1; i >= 0; i-- {
		if tg.sent[i]["method"] == method {
			return tg.sent[i]
		}
	}
	return nil
}

func countSent(tg *fakeTelegram, method, has string) int {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	n := 0
	for _, m := range tg.sent {
		if s, _ := m["text"].(string); m["method"] == method && strings.Contains(s, has) {
			n++
		}
	}
	return n
}

// streamBot is a running bot on a fake Telegram, its chat already on a
// session, in the given streaming mode.
func streamBot(t *testing.T, mode string) (*fakeTelegram, *fakeHost, func()) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	tg := &fakeTelegram{}
	srv := httptest.NewServer(tg)
	prev := apiBase
	apiBase = srv.URL
	_, _ = Change(func(s *Settings) error {
		s.Enabled, s.Token, s.Streaming = true, "1:abc", mode
		s.Allowed = []Person{{ID: 7}}
		s.chat(7).Session = "s1"
		return nil
	})
	host := &fakeHost{events: make(chan gateway.Event, 64), roots: []string{"/work/app"}}
	b := &Bot{Host: host}
	lastBot = b
	b.Start()
	deadline := time.Now().Add(5 * time.Second)
	for b.Status().State != "up" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	return tg, host, func() {
		b.Stop()
		srv.Close()
		apiBase = prev
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Progress mode: a message shows the steps as they happen and folds into a
// summary at the end; the answer comes as a message of its own.
func TestProgressModeShowsStepsThenTheAnswer(t *testing.T) {
	tg, host, stop := streamBot(t, StreamProgress)
	defer stop()
	host.events <- gateway.New(gateway.EvTurnStarted, "s1", gateway.TurnData{Prompt: "fix it"})
	bash := call("t1", "Bash", `{"command":"go test ./..."}`)
	host.events <- gateway.New(gateway.EvToolStart, "s1", gateway.ToolData{Call: bash})
	waitFor(t, "no progress message", func() bool { return countSent(tg, "sendMessage", "💻 Bash") == 1 })
	done := bash
	done.Done = true
	host.events <- gateway.New(gateway.EvToolDone, "s1", gateway.ToolData{Call: done})
	waitFor(t, "the step was not marked done", func() bool { return countSent(tg, "editMessageText", "✅ 💻 Bash") >= 1 })
	host.events <- gateway.New(gateway.EvMessage, "s1", gateway.MessageData{Message: session.Message{Role: session.RoleAssistant, Text: "All **green**."}})
	host.events <- gateway.New(gateway.EvTurnDone, "s1", gateway.TurnData{})
	waitFor(t, "no summary", func() bool { return countSent(tg, "editMessageText", "✅ <b>Done</b>") == 1 })
	waitFor(t, "no answer", func() bool { return countSent(tg, "sendMessage", "All <b>green</b>.") == 1 })
}

// Partial mode: the answer streams into the message, and a short one ends
// there — no second message.
func TestPartialModeStreamsTheAnswerIntoTheMessage(t *testing.T) {
	tg, host, stop := streamBot(t, StreamPartial)
	defer stop()
	host.events <- gateway.New(gateway.EvTurnStarted, "s1", gateway.TurnData{Prompt: "explain"})
	host.events <- gateway.New(gateway.EvTextDelta, "s1", gateway.TextData{Text: "The guard rejects a second submit because "})
	waitFor(t, "the answer did not stream", func() bool { return countSent(tg, "sendMessage", "The guard rejects") == 1 })
	host.events <- gateway.New(gateway.EvTextDelta, "s1", gateway.TextData{Text: "the result must be null first."})
	waitFor(t, "the stream was not edited", func() bool { return countSent(tg, "editMessageText", "must be null") >= 1 })
	host.events <- gateway.New(gateway.EvMessage, "s1", gateway.MessageData{Message: session.Message{Role: session.RoleAssistant, Text: "The guard rejects a second submit because the result must be null first."}})
	host.events <- gateway.New(gateway.EvTurnDone, "s1", gateway.TurnData{})
	waitFor(t, "the final answer did not land in place", func() bool {
		m := lastSent(tg, "editMessageText")
		s, _ := m["text"].(string)
		return strings.Contains(s, "<i>✅ Done") && strings.HasSuffix(s, "null first.")
	})
	time.Sleep(300 * time.Millisecond)
	if n := countSent(tg, "sendMessage", "null first."); n != 0 {
		t.Fatalf("the answer was sent again as %d new message(s)", n)
	}
}

var lastBot *Bot

// Telegram asking to slow down is ridden out: the edit waits its turn and
// lands, the turn edits more slowly, and the status says it happened.
func TestFloodControlIsRiddenOut(t *testing.T) {
	tg, host, stop := streamBot(t, StreamProgress)
	defer stop()
	tg.mu.Lock()
	tg.floodEdits = 1
	tg.mu.Unlock()
	host.events <- gateway.New(gateway.EvTurnStarted, "s1", gateway.TurnData{Prompt: "go"})
	a := call("t1", "Bash", `{"command":"make build"}`)
	host.events <- gateway.New(gateway.EvToolStart, "s1", gateway.ToolData{Call: a})
	waitFor(t, "no progress message", func() bool { return countSent(tg, "sendMessage", "make build") == 1 })
	done := a
	done.Done = true
	host.events <- gateway.New(gateway.EvToolDone, "s1", gateway.ToolData{Call: done})
	waitFor(t, "the edit never landed after the flood wait", func() bool { return countSent(tg, "editMessageText", "✅ 💻 Bash") >= 1 })
	st := lastBot.Status()
	if st.Floods < 1 || st.Skipped < 1 || st.Edits < 1 {
		t.Fatalf("status %+v", st)
	}
}
