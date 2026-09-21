package engine

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// newOpenCode drives the opencode CLI. `opencode run` is non-interactive and
// exposes no sandbox switches, so its own configuration governs what it may do.
func newOpenCode(root string) *CLI {
	return &CLI{
		id: IDOpenCode, label: "opencode", bin: "opencode",
		root: root, fs: vfs.NewLocal(root), approvals: false,
		argv:   opencodeArgv,
		newDec: func() decoder { return &opencodeDec{seen: map[string]bool{}} },
	}
}

func opencodeArgv(c *CLI, t agent.Turn, _ *broker) []string {
	a := []string{"run", "--format", "json"}
	// opencode expresses the difference as an agent rather than a flag: its
	// plan agent has no tools that write.
	if t.Mode == agent.ModePlan {
		a = append(a, "--agent", "plan")
	}
	if root := firstNonEmpty(t.Root, c.root); root != "" {
		a = append(a, "--dir", root)
	}
	if t.ExternalID != "" {
		a = append(a, "-s", t.ExternalID)
		if t.Fork {
			a = append(a, "--fork")
		}
	}
	return append(a, t.PromptText())
}

// opencodeDec parses `opencode run --format json`.
//
// Text arrives as parts that grow in place, so each event carries the whole
// text for its part id; only the new suffix is forwarded as a delta.
type opencodeDec struct {
	sent    bool
	seen    map[string]bool // tool call ids already committed to the transcript
	textID  string
	textSo  string
	pending string
	fail    string
}

func (d *opencodeDec) failure() string { return d.fail }

func (d *opencodeDec) line(raw []byte, emit func(agent.Event)) {
	var ev struct {
		Type      string `json:"type"`
		SessionID string `json:"sessionID"`
		Error     string `json:"error"`
		Part      struct {
			ID     string `json:"id"`
			Type   string `json:"type"`
			Text   string `json:"text"`
			Tool   string `json:"tool"`
			CallID string `json:"callID"`
			Reason string `json:"reason"`
			State  struct {
				Status string          `json:"status"`
				Input  json.RawMessage `json:"input"`
				Output string          `json:"output"`
				Error  string          `json:"error"`
				Title  string          `json:"title"`
			} `json:"state"`
			Tokens struct {
				Input  int64 `json:"input"`
				Output int64 `json:"output"`
				Cache  struct {
					Read int64 `json:"read"`
				} `json:"cache"`
			} `json:"tokens"`
		} `json:"part"`
	}
	if json.Unmarshal(raw, &ev) != nil {
		return
	}
	if ev.SessionID != "" && !d.sent {
		d.sent = true
		emit(agent.EvSession{ExternalID: ev.SessionID})
	}

	switch ev.Type {
	case "text":
		p := ev.Part
		if p.ID != d.textID {
			d.textID, d.textSo = p.ID, ""
		}
		if delta := strings.TrimPrefix(p.Text, d.textSo); delta != "" && len(p.Text) >= len(d.textSo) {
			emit(agent.EvTextDelta{Text: delta})
		}
		d.textSo = p.Text
		d.pending = joinText(d.pending, "")
		d.pending = p.Text

	case "tool_use":
		p := ev.Part
		call := session.ToolCall{
			ID:    firstNonEmpty(p.CallID, p.ID),
			Name:  firstNonEmpty(p.Tool, "tool"),
			Input: p.State.Input,
		}
		if !d.seen[call.ID] {
			d.seen[call.ID] = true
			// The call has to exist in the transcript before its result can be
			// attached to it, and opencode may report a finished tool in one
			// event, so commit it here either way.
			emit(agent.EvAssistant{Message: session.Message{
				Role: session.RoleAssistant, Text: d.pending,
				Tools: []session.ToolCall{call}, At: time.Now(),
			}})
			d.pending, d.textSo, d.textID = "", "", ""
			emit(agent.EvToolStart{Call: call})
		}
		switch p.State.Status {
		case "completed", "error":
			call.Done = true
			call.IsError = p.State.Status == "error"
			call.Result = firstNonEmpty(p.State.Output, p.State.Error, p.State.Title, "ok")
			emit(agent.EvToolDone{Call: call})
		}

	case "step_finish":
		emit(agent.EvUsage{
			In: ev.Part.Tokens.Input, Out: ev.Part.Tokens.Output,
			CacheRead: ev.Part.Tokens.Cache.Read,
		})
		if ev.Part.Reason == "stop" {
			d.flush(emit)
		}

	case "error":
		d.flush(emit)
		d.fail = firstNonEmpty(ev.Error, "the run failed")
	}
}

func (d *opencodeDec) finish(emit func(agent.Event)) { d.flush(emit) }

func (d *opencodeDec) flush(emit func(agent.Event)) {
	if strings.TrimSpace(d.pending) == "" {
		d.pending, d.textSo, d.textID = "", "", ""
		return
	}
	emit(agent.EvAssistant{Message: session.Message{
		Role: session.RoleAssistant, Text: d.pending, At: time.Now(),
	}})
	d.pending, d.textSo, d.textID = "", "", ""
}
