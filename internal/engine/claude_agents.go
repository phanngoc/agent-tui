package engine

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/session"
)

// Sub-agents in Claude Code's stream.
//
// The Agent tool (Task, before) starts a sub-agent. Claude Code reports it
// three ways, all in the one stream:
//
//   - system task_started / task_progress / task_updated / task_notification
//     with task_type "local_agent": its type, description and prompt; what it
//     is doing now ("Reading go.mod"), its last tool, tokens, tool uses and
//     time; how it ended, and its report;
//   - assistant and user messages carrying parent_tool_use_id — the Agent call
//     that started it — which are its own tool calls and their results;
//   - and, for an agent started by a sub-agent, an Agent call inside those.
//
// Its words — what it says it is about to do, between calls — are withheld
// unless CLAUDE_CODE_FORWARD_SUBAGENT_TEXT is set, which newClaude does; they
// then arrive the same way, one block at a time. Nothing finer is sent: the
// CLI streams no token deltas for a sub-agent (measured against 2.1.292, with
// --include-partial-messages and with the option above), so a block as it is
// finished is as live as a sub-agent gets.
//
// Read without parent_tool_use_id, a sub-agent's calls land in the main
// transcript as if the main agent had made them, which is what happened.
// Here they are kept apart, on a tree rooted at each top-level Agent call,
// and the whole tree is sent on every change.

// maxAgentCalls is how many of a sub-agent's calls are kept; maxAgentResult
// how much of each result.
const (
	maxAgentCalls  = 80
	maxAgentResult = 400
)

type subAgentState struct {
	sa     session.SubAgent
	parent string // the Agent call of the agent that started this one, "" for the main agent
}

// isAgentTool says a tool starts a sub-agent.
func isAgentTool(name string) bool { return name == "Agent" || name == "Task" }

func (d *claudeDec) agentState(toolUse string) *subAgentState {
	if d.agents == nil {
		d.agents = map[string]*subAgentState{}
		d.taskAgent = map[string]string{}
	}
	return d.agents[toolUse]
}

// noteAgentCall records an Agent call as it is made, by the main agent
// (parent "") or by a sub-agent.
func (d *claudeDec) noteAgentCall(id, parent string, input json.RawMessage, emit func(agent.Event)) {
	if d.agentState(id) != nil {
		return
	}
	var in struct {
		Description  string `json:"description"`
		Prompt       string `json:"prompt"`
		SubagentType string `json:"subagent_type"`
	}
	_ = json.Unmarshal(input, &in)
	d.agents[id] = &subAgentState{parent: parent, sa: session.SubAgent{
		Type: firstNonEmpty(in.SubagentType, "general-purpose"), Description: in.Description,
		Prompt: in.Prompt, State: "starting", Started: time.Now()}}
	d.emitAgent(id, emit)
}

// taskFields are the parts of a task_* event about a sub-agent.
type taskFields struct {
	Subtype      string `json:"subtype"`
	TaskID       string `json:"task_id"`
	ToolUseID    string `json:"tool_use_id"`
	TaskType     string `json:"task_type"`
	SubagentType string `json:"subagent_type"`
	Description  string `json:"description"`
	Prompt       string `json:"prompt"`
	SpawnDepth   int    `json:"spawn_depth"`
	Background   bool   `json:"is_backgrounded"`
	LastTool     string `json:"last_tool_name"`
	Status       string `json:"status"`
	Summary      string `json:"summary"`
	Usage        struct {
		TotalTokens int64 `json:"total_tokens"`
		ToolUses    int   `json:"tool_uses"`
		DurationMS  int64 `json:"duration_ms"`
	} `json:"usage"`
	Patch struct {
		Status  string `json:"status"`
		EndTime int64  `json:"end_time"`
	} `json:"patch"`
}

// agentTask handles a task_* event when it is about a sub-agent, and says
// whether it was.
func (d *claudeDec) agentTask(raw []byte, emit func(agent.Event)) bool {
	var t taskFields
	if json.Unmarshal(raw, &t) != nil {
		return false
	}
	id := t.ToolUseID
	if id == "" {
		d.agentState("")
		id = d.taskAgent[t.TaskID]
	}
	if id == "" || (t.Subtype == "task_started" && t.TaskType != "local_agent" && t.SubagentType == "") {
		return false
	}
	st := d.agentState(id)
	if st == nil {
		if t.Subtype != "task_started" && d.taskAgent[t.TaskID] == "" {
			return false // a background command, not an agent
		}
		st = &subAgentState{sa: session.SubAgent{State: "starting", Started: time.Now()}}
		d.agents[id] = st
	}
	a := &st.sa
	if t.TaskID != "" {
		a.ID = t.TaskID
		d.taskAgent[t.TaskID] = id
	}
	switch t.Subtype {
	case "task_started":
		a.State = "running"
		a.Type = firstNonEmpty(t.SubagentType, a.Type)
		a.Description = firstNonEmpty(t.Description, a.Description)
		a.Prompt = firstNonEmpty(t.Prompt, a.Prompt)
		a.Depth, a.Background = t.SpawnDepth, t.Background
	case "task_progress":
		if a.State == "starting" {
			a.State = "running"
		}
		// An agent whose call was made in an earlier turn is first seen
		// here, and its type with it.
		a.Type = firstNonEmpty(a.Type, t.SubagentType)
		a.Activity = firstNonEmpty(t.Description, a.Activity)
		a.LastTool = firstNonEmpty(t.LastTool, a.LastTool)
		if t.Usage.TotalTokens > 0 {
			a.Tokens = t.Usage.TotalTokens
		}
		if t.Usage.ToolUses > 0 {
			a.ToolUses = t.Usage.ToolUses
		}
		if t.Usage.DurationMS > 0 {
			a.Duration = time.Duration(t.Usage.DurationMS) * time.Millisecond
		}
	case "task_updated":
		if s := claudeTaskState(t.Patch.Status); s != "running" {
			a.State = s
			a.Ended = endTime(t.Patch.EndTime)
			a.Activity = ""
		}
	case "task_notification":
		if s := claudeTaskState(t.Status); s != "running" {
			a.State = s
			if a.Ended.IsZero() {
				a.Ended = time.Now()
			}
			a.Activity = ""
		}
		a.Summary = firstNonEmpty(t.Summary, a.Summary)
	default:
		return true
	}
	if !a.Running() && a.Duration == 0 && !a.Started.IsZero() && !a.Ended.IsZero() {
		a.Duration = a.Ended.Sub(a.Started)
	}
	d.emitAgent(id, emit)
	return true
}

func endTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Now()
	}
	return time.UnixMilli(ms)
}

// subMessage takes an assistant or user message a sub-agent sent: its own
// calls, their results, and its words, which end as its report.
func (d *claudeDec) subMessage(kind, parent string, raw json.RawMessage, emit func(agent.Event)) {
	st := d.agentState(parent)
	if st == nil {
		// Its Agent call has not been seen (a resumed stream): start a record
		// so its work is not lost.
		st = &subAgentState{sa: session.SubAgent{State: "running", Started: time.Now()}}
		d.agents[parent] = st
	}
	a := &st.sa
	var m struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return
	}
	var blocks []struct {
		Type      string          `json:"type"`
		Text      string          `json:"text"`
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Input     json.RawMessage `json:"input"`
		ToolUseID string          `json:"tool_use_id"`
		IsError   bool            `json:"is_error"`
		Content   json.RawMessage `json:"content"`
	}
	if json.Unmarshal(m.Content, &blocks) != nil {
		return
	}
	changed := false
	for _, b := range blocks {
		switch {
		case kind == "assistant" && b.Type == "tool_use":
			a.Calls = append(a.Calls, session.ToolCall{ID: b.ID, Name: b.Name, Input: b.Input})
			if len(a.Calls) > maxAgentCalls {
				a.Calls = a.Calls[len(a.Calls)-maxAgentCalls:]
			}
			a.LastTool = b.Name
			if a.State == "starting" {
				a.State = "running"
			}
			if isAgentTool(b.Name) {
				d.noteAgentCall(b.ID, parent, b.Input, emit)
			}
			changed = true
		case kind == "assistant" && b.Type == "text" && strings.TrimSpace(b.Text) != "":
			a.Summary = strings.TrimSpace(b.Text)
			changed = true
		case kind == "user" && b.Type == "text" && a.Prompt == "":
			// What it was asked, for an agent whose call was not seen.
			a.Prompt = b.Text
			changed = true
		case kind == "user" && b.Type == "tool_result":
			for i := range a.Calls {
				if a.Calls[i].ID == b.ToolUseID {
					res := flattenContent(b.Content)
					if r := []rune(res); len(r) > maxAgentResult {
						res = string(r[:maxAgentResult]) + "…"
					}
					a.Calls[i].Result, a.Calls[i].IsError, a.Calls[i].Done = res, b.IsError, true
					changed = true
				}
			}
		}
	}
	if changed {
		d.emitAgent(parent, emit)
	}
}

// emitAgent sends the tree the agent at id belongs to, from its top-level
// Agent call down.
func (d *claudeDec) emitAgent(id string, emit func(agent.Event)) {
	root := id
	for seen := 0; seen < 16; seen++ {
		st := d.agents[root]
		if st == nil || st.parent == "" {
			break
		}
		root = st.parent
	}
	if d.agents[root] == nil {
		return
	}
	emit(agent.EvSubAgent{ToolUse: root, Agent: d.snapshot(root, 0)})
}

// snapshot copies an agent and, through its Agent calls, the agents under it.
func (d *claudeDec) snapshot(id string, depth int) session.SubAgent {
	a := d.agents[id].sa
	a.Calls = append([]session.ToolCall(nil), a.Calls...)
	if depth < 8 {
		for i, c := range a.Calls {
			if isAgentTool(c.Name) && d.agents[c.ID] != nil {
				child := d.snapshot(c.ID, depth+1)
				a.Calls[i].Agent = &child
			}
		}
	}
	return a
}
