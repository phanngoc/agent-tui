package engine

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// newCodex drives the Codex CLI. `codex exec` is non-interactive and has no
// approval channel, so the only real control is its sandbox policy.
func newCodex(root string) *CLI {
	return &CLI{
		id: IDCodex, label: "Codex", bin: "codex",
		root: root, fs: vfs.NewLocal(root), approvals: false,
		argv:   codexArgv,
		newDec: func() decoder { return &codexDec{} },
	}
}

// codexArgv builds the command line.
//
// `codex exec` and its `resume` / `fork` subcommands do not take the same
// flags: both subcommands reject -C and --sandbox outright, which made every
// turn after the first fail with "unexpected argument '-C' found". Only flags
// the chosen subcommand accepts are passed; the working directory is set on the
// process either way, so a continued session still runs in the right place.
func codexArgv(c *CLI, t agent.Turn, _ *broker) []string {
	// `resume` and `fork` share the same narrower flag set.
	continuing := t.ExternalID != ""

	a := []string{"exec"}
	switch {
	case continuing && t.Fork:
		a = append(a, "fork", t.ExternalID)
	case continuing:
		a = append(a, "resume", t.ExternalID)
	}
	a = append(a, "--json", "--skip-git-repo-check")

	if !continuing {
		if root := firstNonEmpty(t.Root, c.root); root != "" {
			a = append(a, "-C", root)
		}
	}
	switch {
	case t.Mode == agent.ModeFull:
		// Accepted by every subcommand.
		a = append(a, "--dangerously-bypass-approvals-and-sandbox")
	case !continuing:
		// codex exec cannot ask, so the sandbox is the whole of the policy:
		// plan reads only, everything else is confined to the project. A
		// resumed session keeps the sandbox it was started with.
		sandbox := "workspace-write"
		if t.Mode == agent.ModePlan {
			sandbox = "read-only"
		}
		a = append(a, "--sandbox", sandbox)
	}
	return append(a, t.Prompt)
}

// codexDec parses `codex exec --json`.
//
// Codex reports whole items rather than deltas: an agent_message arrives
// complete, and tool work arrives as item.started/item.completed pairs. Text is
// buffered so a message and the tool call it introduces land in one transcript
// block instead of two.
type codexDec struct {
	pending string
	fail    string
	sent    bool
}

func (d *codexDec) failure() string { return d.fail }

func (d *codexDec) line(raw []byte, emit func(agent.Event)) {
	var ev struct {
		Type     string `json:"type"`
		ThreadID string `json:"thread_id"`
		Error    struct {
			Message string `json:"message"`
		} `json:"error"`
		Usage struct {
			InputTokens       int64 `json:"input_tokens"`
			CachedInputTokens int64 `json:"cached_input_tokens"`
			OutputTokens      int64 `json:"output_tokens"`
		} `json:"usage"`
		Item codexItem `json:"item"`
	}
	if json.Unmarshal(raw, &ev) != nil {
		return
	}

	switch ev.Type {
	case "thread.started":
		if ev.ThreadID != "" && !d.sent {
			d.sent = true
			emit(agent.EvSession{ExternalID: ev.ThreadID})
		}
	case "turn.started":
		emit(agent.EvStatus{Text: "thinking"})

	case "item.started":
		if call, ok := ev.Item.toolCall(); ok {
			// Commit the text so far together with the call it introduces, so
			// the transcript reads as one block.
			emit(agent.EvAssistant{Message: session.Message{
				Role: session.RoleAssistant, Text: d.pending,
				Tools: []session.ToolCall{call}, At: time.Now(),
			}})
			d.pending = ""
			emit(agent.EvToolStart{Call: call})
		}

	case "item.completed":
		switch ev.Item.Type {
		case "agent_message":
			d.pending = joinText(d.pending, ev.Item.Text)
		case "reasoning":
			emit(agent.EvThinkingDelta{Text: ev.Item.Text})
		default:
			if call, ok := ev.Item.toolCall(); ok {
				call.Done = true
				call.Result, call.IsError = ev.Item.outcome()
				emit(agent.EvToolDone{Call: call})
			}
		}

	case "turn.completed":
		d.flush(emit)
		emit(agent.EvUsage{
			In: ev.Usage.InputTokens, Out: ev.Usage.OutputTokens,
			CacheRead: ev.Usage.CachedInputTokens,
		})

	case "turn.failed", "error":
		d.flush(emit)
		d.fail = firstNonEmpty(ev.Error.Message, "the turn failed")
	}
}

func (d *codexDec) finish(emit func(agent.Event)) { d.flush(emit) }

func (d *codexDec) flush(emit func(agent.Event)) {
	if strings.TrimSpace(d.pending) == "" {
		d.pending = ""
		return
	}
	emit(agent.EvAssistant{Message: session.Message{
		Role: session.RoleAssistant, Text: d.pending, At: time.Now(),
	}})
	d.pending = ""
}

// codexItem covers the item shapes codex emits. Unknown types are still shown
// as tool calls, which degrades better than dropping them.
type codexItem struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	Command  string          `json:"command"`
	Output   string          `json:"aggregated_output"`
	ExitCode *int            `json:"exit_code"`
	Status   string          `json:"status"`
	Changes  json.RawMessage `json:"changes"`
	Path     string          `json:"path"`
}

// toolCall maps an item onto the transcript's tool vocabulary.
func (it codexItem) toolCall() (session.ToolCall, bool) {
	switch it.Type {
	case "", "agent_message", "reasoning", "todo_list":
		return session.ToolCall{}, false
	}
	name := it.Type
	input := map[string]any{}
	switch it.Type {
	case "command_execution":
		name = "bash"
		input["command"] = it.Command
	case "file_change", "patch_apply":
		name = "edit_file"
		if it.Path != "" {
			input["path"] = it.Path
		} else if len(it.Changes) > 0 {
			input["changes"] = json.RawMessage(it.Changes)
		}
	case "web_search":
		input["query"] = it.Text
	default:
		if it.Text != "" {
			input["detail"] = it.Text
		}
	}
	blob, _ := json.Marshal(input)
	return session.ToolCall{ID: "codex:" + it.ID, Name: name, Input: blob}, true
}

func (it codexItem) outcome() (string, bool) {
	isErr := it.Status == "failed" || (it.ExitCode != nil && *it.ExitCode != 0)
	out := firstNonEmpty(it.Output, it.Text)
	if out == "" {
		if isErr {
			out = "failed"
		} else {
			out = "ok"
		}
	}
	return out, isErr
}
