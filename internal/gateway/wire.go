// Package gateway connects every place a conversation can be seen or driven —
// the terminal app, the desktop window that hosts it, and the web admin — so
// they show the same thing at the same moment.
//
// One process, `agent-tui serve`, is the gateway. It owns no special engine:
// it runs turns with the same registry, the same kit (memory, skills, MCP) and
// the same learner as the terminal app. What it adds is a hub. Every process
// that runs turns publishes what happens in them as Events, in one
// vocabulary; the hub fans them out to every web client and keeps enough of
// the live state that a page opened mid-turn sees the turn so far.
//
// A session is run by whoever holds it. A conversation open in a terminal
// belongs to that terminal: a prompt, a cancel or an approval sent from the
// web is routed to it as a Command and handled there exactly as if it had
// been typed, so there is only ever one writer of a session. A conversation
// no terminal holds is run by the gateway itself.
package gateway

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/session"
)

// Event types.
const (
	EvPeerJoined      = "peer.joined"
	EvPeerLeft        = "peer.left"
	EvSessionUpdated  = "session.updated"
	EvSessionDeleted  = "session.deleted"
	EvTurnStarted     = "turn.started"
	EvMessage         = "message"
	EvTextDelta       = "text.delta"
	EvThinkingDelta   = "thinking.delta"
	EvStatus          = "status"
	EvToolPending     = "tool.pending"
	EvToolStart       = "tool.start"
	EvToolOutput      = "tool.output"
	EvToolDone        = "tool.done"
	EvApprovalRequest = "approval.request"
	EvApprovalDone    = "approval.resolved"
	EvChoiceRequest   = "choice.request"
	EvChoiceDone      = "choice.resolved"
	EvUsage           = "usage"
	EvTurnDone        = "turn.done"
	EvSubAgent        = "subagent"
	EvTrace           = "trace"
	EvLearn           = "learn"
	EvConfig          = "config.changed"
)

// Event is one thing that happened, in any process.
type Event struct {
	Seq     int64           `json:"seq"`
	At      time.Time       `json:"at"`
	Type    string          `json:"type"`
	Session string          `json:"session,omitempty"`
	Root    string          `json:"root,omitempty"`
	Origin  string          `json:"origin,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// New builds an event with its data encoded.
func New(typ, sessionID string, data any) Event {
	e := Event{At: time.Now().UTC(), Type: typ, Session: sessionID}
	if data != nil {
		e.Data, _ = json.Marshal(data)
	}
	return e
}

// Command types, gateway to peer.
const (
	CmdPrompt  = "prompt"
	CmdCancel  = "cancel"
	CmdApprove = "approve"
	CmdChoose  = "choose"
	CmdReload  = "reload"
	// CmdSettings changes what a session runs on — model, mode, engine —
	// from its next turn.
	CmdSettings = "settings"
	// CmdOpen tells a terminal working in a project that a conversation in
	// it was started or continued elsewhere, so it can list it too.
	CmdOpen = "open"
)

// Command asks the process holding a session to do something to it.
type Command struct {
	Type    string `json:"type"`
	Session string `json:"session"`
	Text    string `json:"text,omitempty"`
	// ID names the approval or choice being answered.
	ID string `json:"id,omitempty"`
	// Verdict is deny, allow or allow_all; Index is the chosen option.
	Verdict string `json:"verdict,omitempty"`
	Index   int    `json:"index,omitempty"`
	From    string `json:"from,omitempty"`
	// Model, Mode and Engine are a settings command's; empty leaves one as
	// it is.
	Model  string `json:"model,omitempty"`
	Mode   string `json:"mode,omitempty"`
	Engine string `json:"engine,omitempty"`
	// Fresh starts a prompt's turn with a context of its own, in a session
	// that keeps what came before for the reader (a scheduled job's run).
	Fresh bool `json:"fresh,omitempty"`
}

// SubAgentData is a sub-agent tree, from the top-level call that started it.
type SubAgentData struct {
	ToolUse string           `json:"tool_use"`
	Agent   session.SubAgent `json:"agent"`
}

// Summary is a session as a list shows it.
type Summary struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Root     string    `json:"root"`
	Engine   string    `json:"engine,omitempty"`
	Model    string    `json:"model,omitempty"`
	Mode     string    `json:"mode,omitempty"`
	Target   string    `json:"target,omitempty"`
	CWD      string    `json:"cwd,omitempty"`
	Messages int       `json:"messages"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
	Busy     bool      `json:"busy"`
	Status   string    `json:"status,omitempty"`
	Owner    string    `json:"owner,omitempty"`
	InTokens int64     `json:"input_tokens"`
	OutToks  int64     `json:"output_tokens"`
	Closed   bool      `json:"closed,omitempty"`
	SideOf   string    `json:"side_of,omitempty"`
	// Job is the scheduled job the session is a run of.
	Job string `json:"job,omitempty"`
}

// SummaryOf summarises a session.
func SummaryOf(s *session.Session) Summary {
	return Summary{
		ID: s.ID, Title: s.Label(), Root: s.Root, Engine: s.Engine, Model: s.Model, Mode: s.Mode,
		Target: s.Target, CWD: s.CWD, Messages: len(s.Messages), Created: s.Created, Updated: s.Updated,
		Busy: s.Busy, Status: s.Status, InTokens: s.InputTokens, OutToks: s.OutputTokens,
		Closed: s.Closed, SideOf: s.SideOf, Job: jobOf(s),
	}
}

// jobOf is the job a session is a run of. Runs from before sessions said so
// are known by the header the scheduler starts their prompt with.
func jobOf(s *session.Session) string {
	if s.Job != "" {
		return s.Job
	}
	if len(s.Messages) > 0 && strings.HasPrefix(s.Messages[0].Text, "[scheduled: ") {
		return "scheduled"
	}
	return ""
}

// Payloads.
type (
	TextData struct {
		Text string `json:"text"`
	}
	MessageData struct {
		Index   int             `json:"index"`
		Message session.Message `json:"message"`
	}
	ToolsData struct {
		Calls []session.ToolCall `json:"calls"`
	}
	ToolData struct {
		Call session.ToolCall `json:"call"`
	}
	OutputData struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	ApprovalData struct {
		ID     string           `json:"id"`
		Call   session.ToolCall `json:"call"`
		Reason string           `json:"reason"`
	}
	ChoiceData struct {
		ID       string           `json:"id"`
		Call     session.ToolCall `json:"call"`
		Question string           `json:"question"`
		Options  []session.Choice `json:"options"`
	}
	ResolvedData struct {
		ID      string `json:"id"`
		Verdict string `json:"verdict,omitempty"`
		Index   int    `json:"index,omitempty"`
		By      string `json:"by,omitempty"`
	}
	UsageData struct {
		In        int64 `json:"in"`
		Out       int64 `json:"out"`
		CacheRead int64 `json:"cache_read"`
	}
	TurnData struct {
		Prompt string `json:"prompt,omitempty"`
		Engine string `json:"engine,omitempty"`
		Error  string `json:"error,omitempty"`
	}
)

// FromAgent translates an engine event. Approvals and choices are left to
// the caller, which alone knows how it will answer them; nil means "not
// published".
func FromAgent(sessionID string, ev agent.Event) *Event {
	var e Event
	switch v := ev.(type) {
	case agent.EvStatus:
		e = New(EvStatus, sessionID, TextData{v.Text})
	case agent.EvTextDelta:
		e = New(EvTextDelta, sessionID, TextData{v.Text})
	case agent.EvThinkingDelta:
		e = New(EvThinkingDelta, sessionID, TextData{v.Text})
	case agent.EvToolPending:
		e = New(EvToolPending, sessionID, ToolsData{v.Calls})
	case agent.EvToolStart:
		e = New(EvToolStart, sessionID, ToolData{v.Call})
	case agent.EvToolOutput:
		e = New(EvToolOutput, sessionID, OutputData{v.ID, v.Text})
	case agent.EvToolDone:
		e = New(EvToolDone, sessionID, ToolData{v.Call})
	case agent.EvUsage:
		e = New(EvUsage, sessionID, UsageData{v.In, v.Out, v.CacheRead})
	case agent.EvSubAgent:
		e = New(EvSubAgent, sessionID, SubAgentData{ToolUse: v.ToolUse, Agent: v.Agent})
	case agent.EvDone:
		d := TurnData{}
		if v.Err != nil {
			d.Error = v.Err.Error()
		}
		e = New(EvTurnDone, sessionID, d)
	default:
		return nil
	}
	return &e
}

// VerdictOf parses a verdict name.
func VerdictOf(s string) agent.Verdict {
	switch s {
	case "allow":
		return agent.Allow
	case "allow_all":
		return agent.AllowAll
	}
	return agent.Deny
}

// VerdictName names a verdict.
func VerdictName(v agent.Verdict) string {
	switch v {
	case agent.Allow:
		return "allow"
	case agent.AllowAll:
		return "allow_all"
	}
	return "deny"
}
