package agent

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// Event is what the agent publishes while a turn runs. The UI applies each one
// to its model; the agent itself never touches session state, which keeps the
// whole thing free of data races without a single mutex.
type Event any

type (
	// EvStatus updates the one-line status shown while the agent works.
	EvStatus struct{ Text string }
	// EvTextDelta is a chunk of the assistant's visible answer.
	EvTextDelta struct{ Text string }
	// EvThinkingDelta is a chunk of summarised reasoning.
	EvThinkingDelta struct{ Text string }
	// EvAssistant commits a finished assistant turn, tool calls included.
	EvAssistant struct{ Message session.Message }
	// EvApproval asks the user to allow a mutating tool. Reply exactly once.
	EvApproval struct {
		Call session.ToolCall
		// Reason says why this one is being asked about when the mode would
		// normally act on its own.
		Reason string
		Reply  chan Verdict
	}
	// EvChoice asks the user to pick between options the agent proposed.
	// Reply exactly once with the chosen index, or -1 to decline.
	EvChoice struct {
		Call     session.ToolCall
		Question string
		Options  []session.Choice
		Reply    chan int
	}
	// EvToolPending carries the tool calls of the turn being written right
	// now, while the model is still emitting them. Their inputs are half a
	// JSON object and are meant to be read as such: the point is to see the
	// path or the command appear, not to act on it. The same calls arrive
	// again, complete, on the EvAssistant that ends the turn.
	EvToolPending struct{ Calls []session.ToolCall }
	// EvToolOutput is what a running tool has printed so far. A build or a
	// test run is the reason to watch a turn at all, and it has nothing to say
	// until it exits unless someone forwards it.
	EvToolOutput struct{ ID, Text string }
	// EvToolStart marks a tool as running.
	EvToolStart struct{ Call session.ToolCall }
	// EvToolDone carries the tool's outcome.
	EvToolDone struct{ Call session.ToolCall }
	// EvUsage reports token accounting for one request.
	EvUsage struct{ In, Out, CacheRead int64 }
	// EvSession carries an external agent's own session identifier as soon as
	// it is known, so the conversation can be resumed later.
	EvSession struct{ ExternalID string }
	// EvTask reports a background command an external agent started, so it
	// appears beside the ones this process runs.
	EvTask struct {
		ID     string
		Label  string
		State  string // "", "running", "done", "failed", "stopped"
		Note   string
		Output string // a file the agent is writing the output to, if any
	}
	// EvDone ends the turn. Err is nil on success. State is whatever the engine
	// wants handed back on the next turn (SDK message history, an external
	// session id); it travels through the event stream rather than a callback
	// so only the UI goroutine ever touches session state.
	EvDone struct {
		Err   error
		State any
	}
)

// Verdict is the answer to an approval prompt.
type Verdict int

const (
	// Deny refuses this call.
	Deny Verdict = iota
	// Allow permits this call only.
	Allow
	// AllowAll permits this call and stops asking for the rest of the run.
	//
	// It is scoped to the run rather than stored on the agent: engines are
	// shared between concurrent sessions, and trusting one conversation must
	// not quietly trust the others.
	AllowAll
)

// Turn is one request to an engine: the new prompt, the conversation so far,
// and whatever state the engine returned last time.
type Turn struct {
	Prompt  string
	History []session.Message
	State   any
	// ExternalID is the engine's own session id from a previous turn, used to
	// resume rather than start a fresh conversation.
	ExternalID string
	// Fork branches off ExternalID into a new conversation instead of
	// continuing it, so the original is left untouched.
	Fork bool
	Root string
	// Mode is how much the agent may do without asking on this turn.
	Mode Mode
	// Model is the model this session runs on. It belongs to the turn rather
	// than the engine because sessions choose independently, and two sessions
	// on different models run side by side.
	Model string
	// FS is where this turn's work happens. A session aimed at a container
	// runs its agent there, so the agent edits the files the user is looking at.
	FS vfs.FS
	// Files are what the user attached to this prompt.
	Files []session.Attachment
}

// PromptText is the prompt as an engine that can only be handed text should see
// it. An engine with no way to carry an image is given the path to it instead:
// every CLI here can open a file, and a path it can open beats an attachment it
// cannot receive.
//
// The path has to be one that side can open. A CLI running inside a container
// is told where the file was copied to in there, never where it sits on this
// machine — and an image that never got within its reach is left out, because
// naming a path it cannot open sends it looking for a file that is not there,
// which is worse than not mentioning the image at all.
func (t Turn) PromptText() string {
	if len(t.Files) == 0 {
		return t.Prompt
	}
	remote := t.FS != nil && !t.FS.IsLocal()
	var b strings.Builder
	b.WriteString(t.Prompt)
	for _, f := range t.Files {
		where := f.Path
		if remote {
			where = f.Ref
		}
		if where == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("Attached image: " + where)
	}
	return b.String()
}

// Engine runs a turn and reports progress as events. Run owns out and closes it
// when the turn ends. Implementations live in internal/engine; the interface
// sits here so the UI depends only on the event vocabulary.
type Engine interface {
	// ID is the stable key persisted with a session ("api", "claude", ...).
	ID() string
	// Label is what the UI shows.
	Label() string
	// Detail is a short status line: a version, or why the engine is unusable.
	Detail() string
	// Available reports whether this engine can run right now.
	Available() bool
	// CanAsk reports whether this engine is able to route tool approvals back
	// to the UI at all. When false, "ask" mode cannot be honoured and the UI
	// says so rather than implying a gate that does not exist.
	CanAsk() bool
	// Run executes one turn.
	Run(ctx context.Context, t Turn, out chan<- Event)
}

// Agent drives one conversation turn at a time.
type Agent struct {
	client anthropic.Client
	exec   *Executor

	Model     string
	Effort    anthropic.OutputConfigEffort
	MaxTokens int64
	MaxSteps  int
}

// New builds an agent. An empty apiKey falls back to the SDK's own credential
// resolution (ANTHROPIC_API_KEY, then any profile written by `ant auth login`).
func New(apiKey string, exec *Executor, model, effort string, maxTokens int64) *Agent {
	var opts []option.RequestOption
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	ef := anthropic.OutputConfigEffortHigh
	switch effort {
	case "low":
		ef = anthropic.OutputConfigEffortLow
	case "medium":
		ef = anthropic.OutputConfigEffortMedium
	case "xhigh":
		ef = anthropic.OutputConfigEffortXhigh
	case "max":
		ef = anthropic.OutputConfigEffortMax
	}
	return &Agent{
		client:    anthropic.NewClient(opts...),
		exec:      exec,
		Model:     model,
		Effort:    ef,
		MaxTokens: maxTokens,
		MaxSteps:  40,
	}
}

const systemPrompt = `You are the coding agent inside agent-tui, a terminal IDE.

The user is looking at a file tree, a transcript and a syntax-highlighted preview
pane, so keep prose short and let the tools do the talking.

Working rules:
- Read before you write. Use grep and find_files to locate code rather than
  guessing at paths.
- Prefer edit_file over write_file for existing files; write_file replaces the
  whole file.
- write_file, edit_file and bash need the user's approval, which may be denied.
  If a call is denied, do not retry it; say what you would have done instead.
- Paths are relative to the project root. Nothing outside the root is reachable.
- Report what you actually did. If a command failed, say so and show the output.`

// Prepare seeds a session's live history from its persisted messages. Call it
// once per session before the first Run.
func (a *Agent) Prepare(s *session.Session) []anthropic.MessageParam {
	if h, ok := s.Live.([]anthropic.MessageParam); ok {
		return h
	}
	h := Replay(s.Messages)
	s.Live = h
	return h
}

// UserBlocks builds the content of one user message: the images first, then
// what was typed, which is the order the API asks for and the order a reader
// would use anyway — you look at the screenshot, then at the question about it.
//
// An attachment whose file has gone is dropped rather than fatal. The bytes
// live outside the session, and a transcript that cannot be resumed because a
// temporary file was swept up is worse than one that loses a picture.
func UserBlocks(text string, files []session.Attachment) []anthropic.ContentBlockParamUnion {
	blocks := make([]anthropic.ContentBlockParamUnion, 0, len(files)+1)
	for _, f := range files {
		data, err := os.ReadFile(f.Path)
		if err != nil || len(data) == 0 {
			continue
		}
		media := f.Media
		if media == "" {
			media = "image/png"
		}
		blocks = append(blocks, anthropic.NewImageBlockBase64(media,
			base64.StdEncoding.EncodeToString(data)))
	}
	if strings.TrimSpace(text) != "" {
		blocks = append(blocks, anthropic.NewTextBlock(text))
	}
	return blocks
}

// Replay rebuilds API history from persisted messages. Thinking blocks are not
// restored: they carry signatures we do not persist, and a resumed session
// starts a fresh reasoning context anyway.
func Replay(msgs []session.Message) []anthropic.MessageParam {
	out := make([]anthropic.MessageParam, 0, len(msgs)*2)
	for _, m := range msgs {
		if m.Role == session.RoleUser {
			if blocks := UserBlocks(m.Text, m.Files); len(blocks) > 0 {
				out = append(out, anthropic.NewUserMessage(blocks...))
			}
			continue
		}
		blocks := make([]anthropic.ContentBlockParamUnion, 0, 1+len(m.Tools))
		if strings.TrimSpace(m.Text) != "" {
			blocks = append(blocks, anthropic.NewTextBlock(m.Text))
		}
		for _, t := range m.Tools {
			var in any = map[string]any{}
			if len(t.Input) > 0 {
				_ = json.Unmarshal(t.Input, &in)
			}
			blocks = append(blocks, anthropic.NewToolUseBlock(t.ID, in, t.Name))
		}
		if len(blocks) == 0 {
			continue
		}
		out = append(out, anthropic.NewAssistantMessage(blocks...))

		results := make([]anthropic.ContentBlockParamUnion, 0, len(m.Tools))
		for _, t := range m.Tools {
			res := t.Result
			if res == "" {
				res = "(no result recorded)"
			}
			results = append(results, anthropic.NewToolResultBlock(t.ID, res, t.IsError))
		}
		if len(results) > 0 {
			out = append(out, anthropic.NewUserMessage(results...))
		}
	}
	return out
}

// Run executes one full turn: request, tool calls, follow-up requests, until
// the model stops asking for tools. It closes out when it returns.
//
// history is the session's live SDK history including the new user message. The
// updated history comes back on EvDone, so the caller can store it from its own
// goroutine and nothing is shared across the boundary.
func (a *Agent) Run(ctx context.Context, history []anthropic.MessageParam, mode Mode, model string, out chan<- Event) {
	defer close(out)

	// Trust granted at an approval prompt lasts for this run and no longer.
	trusted := false

	send := func(e Event) bool {
		select {
		case out <- e:
			return true
		case <-ctx.Done():
			return false
		}
	}

	spec := ModelFor(cmp.Or(model, a.Model))

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(spec.ID),
		MaxTokens: a.MaxTokens,
		System: []anthropic.TextBlockParam{{
			Text:         systemPrompt + mode.prompt(),
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}},
		Tools:    a.exec.Defs(mode),
		Thinking: thinkingFor(spec, a.MaxTokens),
		Messages: history,
	}
	// Effort is not universal: a model that does not take one rejects the
	// request outright rather than ignoring the field.
	if spec.Effort {
		params.OutputConfig = anthropic.OutputConfigParam{Effort: a.Effort}
	}

	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			buf = buf[:runtime.Stack(buf, false)]
			send(EvDone{Err: fmt.Errorf("agent panic: %v\n%s", r, buf)})
		}
	}()

	for step := 0; step < a.MaxSteps; step++ {
		if ctx.Err() != nil {
			send(EvDone{Err: ctx.Err(), State: params.Messages})
			return
		}
		send(EvStatus{Text: "thinking"})

		msg, err := a.stream(ctx, params, send)
		if err != nil {
			send(EvDone{Err: err, State: params.Messages})
			return
		}

		send(EvUsage{
			In:        msg.Usage.InputTokens,
			Out:       msg.Usage.OutputTokens,
			CacheRead: msg.Usage.CacheReadInputTokens,
		})

		// Preserve the assistant turn verbatim: thinking blocks keep their
		// signatures, which the API requires when tool results follow.
		params.Messages = append(params.Messages, msg.ToParam())

		turn := session.Message{Role: session.RoleAssistant, At: time.Now()}
		var pending []session.ToolCall
		for _, block := range msg.Content {
			switch v := block.AsAny().(type) {
			case anthropic.TextBlock:
				turn.Text += v.Text
			case anthropic.ThinkingBlock:
				turn.Thinking += v.Thinking
			case anthropic.ToolUseBlock:
				pending = append(pending, session.ToolCall{
					ID:    v.ID,
					Name:  v.Name,
					Input: json.RawMessage(v.JSON.Input.Raw()),
				})
			}
		}

		if msg.StopReason == anthropic.StopReasonRefusal {
			turn.Err = "the model declined this request"
			if msg.StopDetails.Explanation != "" {
				turn.Err += ": " + msg.StopDetails.Explanation
			}
		}
		turn.Tools = pending
		send(EvAssistant{Message: turn})

		if len(pending) == 0 || msg.StopReason != anthropic.StopReasonToolUse {
			send(EvDone{State: params.Messages})
			return
		}

		results := make([]anthropic.ContentBlockParamUnion, 0, len(pending))
		for _, call := range pending {
			done := a.runTool(ctx, call, mode, &trusted, send)
			results = append(results, anthropic.NewToolResultBlock(done.ID, done.Result, done.IsError))
		}
		params.Messages = append(params.Messages, anthropic.NewUserMessage(results...))
	}

	send(EvDone{
		Err:   fmt.Errorf("stopped after %d steps without finishing", a.MaxSteps),
		State: params.Messages,
	})
}

// runTool asks for approval when needed, executes, and reports both ends.
func (a *Agent) runTool(ctx context.Context, call session.ToolCall, mode Mode,
	trusted *bool, send func(Event) bool) session.ToolCall {

	if call.Name == askUserTool {
		return a.askUser(ctx, call, send)
	}
	ask, reason := a.exec.ShouldAsk(call, mode, *trusted)
	if ask {
		reply := make(chan Verdict, 1)
		if !send(EvApproval{Call: call, Reason: reason, Reply: reply}) {
			call.Done, call.IsError, call.Result = true, true, "cancelled"
			return call
		}
		select {
		case verdict := <-reply:
			if verdict == AllowAll {
				*trusted = true
			}
			if verdict == Deny {
				call.Done, call.Denied, call.IsError = true, true, true
				call.Result = "The user denied this tool call. Do not retry it; " +
					"explain what you would have done, or propose another approach."
				send(EvToolDone{Call: call})
				return call
			}
		case <-ctx.Done():
			call.Done, call.IsError, call.Result = true, true, "cancelled"
			return call
		}
	}

	send(EvToolStart{Call: call})
	send(EvStatus{Text: call.Name})

	start := time.Now()
	// The sink is how a command's output reaches the transcript while it still
	// has somewhere to go. Only bash writes to it; every other tool answers in
	// one piece and has nothing to stream.
	res, isErr := a.exec.Run(ctx, call.Name, call.Input, func(chunk string) {
		send(EvToolOutput{ID: call.ID, Text: chunk})
	})
	call.Result, call.IsError = res, isErr
	call.Elapsed = time.Since(start)
	call.Done = true

	send(EvToolDone{Call: call})
	return call
}

// askUser puts a question to the user instead of running anything.
//
// It is handled here rather than in the executor so the executor stays free of
// any notion of a user interface: the agent already owns the one channel that
// can reach one.
func (a *Agent) askUser(ctx context.Context, call session.ToolCall, send func(Event) bool) session.ToolCall {
	var in struct {
		Question string `json:"question"`
		Options  []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
	}
	call.Done = true
	if err := json.Unmarshal(call.Input, &in); err != nil {
		call.IsError, call.Result = true, "malformed question: "+err.Error()
		send(EvToolDone{Call: call})
		return call
	}
	if len(in.Options) == 0 {
		call.IsError, call.Result = true, "ask_user needs at least one option"
		send(EvToolDone{Call: call})
		return call
	}

	opts := make([]session.Choice, len(in.Options))
	for i, o := range in.Options {
		opts[i] = session.Choice{Label: o.Label, Detail: o.Description}
	}

	reply := make(chan int, 1)
	if !send(EvChoice{Call: call, Question: in.Question, Options: opts, Reply: reply}) {
		call.IsError, call.Result = true, "cancelled"
		return call
	}

	select {
	case pick := <-reply:
		if pick < 0 || pick >= len(opts) {
			call.IsError = true
			call.Result = "The user dismissed the question without choosing. " +
				"Ask again only if you cannot proceed without an answer."
		} else {
			call.Chosen = opts[pick].Label
			call.Result = "The user chose: " + opts[pick].Label
		}
	case <-ctx.Done():
		call.IsError, call.Result = true, "cancelled"
	}
	send(EvToolDone{Call: call})
	return call
}

// stream performs one request, forwarding deltas as they arrive and
// accumulating the complete message for the caller.
func (a *Agent) stream(ctx context.Context, params anthropic.MessageNewParams,
	send func(Event) bool) (*anthropic.Message, error) {

	st := a.client.Messages.NewStreaming(ctx, params)
	var msg anthropic.Message
	var pending pendingCalls

	for st.Next() {
		ev := st.Current()
		if err := msg.Accumulate(ev); err != nil {
			return nil, err
		}
		switch e := ev.AsAny().(type) {
		case anthropic.ContentBlockDeltaEvent:
			switch d := e.Delta.AsAny().(type) {
			case anthropic.TextDelta:
				if d.Text != "" && !send(EvTextDelta{Text: d.Text}) {
					return nil, ctx.Err()
				}
			case anthropic.ThinkingDelta:
				if d.Thinking != "" && !send(EvThinkingDelta{Text: d.Thinking}) {
					return nil, ctx.Err()
				}
			case anthropic.InputJSONDelta:
				// The call's arguments arrive as a run of JSON fragments. They
				// are forwarded as they come so a long write shows the file it
				// is writing while it writes it.
				if pending.grow(e.Index, d.PartialJSON) {
					send(EvToolPending{Calls: pending.calls()})
				}
			}
		case anthropic.ContentBlockStartEvent:
			if e.ContentBlock.Type == "tool_use" {
				send(EvStatus{Text: "calling " + e.ContentBlock.Name})
				pending.start(e.Index, e.ContentBlock.ID, e.ContentBlock.Name)
				send(EvToolPending{Calls: pending.calls()})
			}
		}
	}
	if err := st.Err(); err != nil {
		return nil, friendly(err)
	}
	return &msg, nil
}

// friendly turns the most common API failures into something actionable.
func friendly(err error) error {
	var apierr *anthropic.Error
	if errors.As(err, &apierr) {
		switch apierr.StatusCode {
		case 401, 403:
			return fmt.Errorf("authentication failed (%d): set ANTHROPIC_API_KEY or run `ant auth login`", apierr.StatusCode)
		case 404:
			return fmt.Errorf("model not found: check the model id in your config")
		case 429:
			return fmt.Errorf("rate limited: wait a moment and retry")
		case 529:
			return fmt.Errorf("the API is overloaded: retry shortly")
		}
		if apierr.StatusCode >= 500 {
			return fmt.Errorf("server error %d: retry shortly", apierr.StatusCode)
		}
		return fmt.Errorf("API error %d: %s", apierr.StatusCode, apierr.Error())
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	return err
}
