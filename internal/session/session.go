// Package session models a conversation and owns its on-disk persistence.
// It deliberately knows nothing about the Anthropic SDK: the agent converts
// these plain structs into API params, which keeps saved sessions readable and
// independent of any SDK version.
package session

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/phanngoc/agent-tui/internal/vfs"
)

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Choice is one option the agent offered the user.
type Choice struct {
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
}

// ToolCall records one tool invocation and its outcome.
type ToolCall struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Input   json.RawMessage `json:"input"`
	Result  string          `json:"result,omitempty"`
	IsError bool            `json:"is_error,omitempty"`
	Done    bool            `json:"done"`
	Denied  bool            `json:"denied,omitempty"`
	// Chosen records what the user picked when the call was a question.
	Chosen  string        `json:"chosen,omitempty"`
	Elapsed time.Duration `json:"elapsed,omitempty"`
	// Agent is the sub-agent the call started, when it is a call that starts
	// one (Claude Code's Agent tool): what it is doing, what it has used,
	// and its own calls, with any agents it started in turn.
	Agent *SubAgent `json:"agent,omitempty"`
}

// SubAgent is an agent another agent started to do part of its work, as
// Claude Code's Agent tool does — an Explore search, a general-purpose job —
// running beside it, often several at once.
type SubAgent struct {
	ID          string `json:"id,omitempty"` // the engine's task id
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
	Prompt      string `json:"prompt,omitempty"`
	// State is starting, running, done, failed or stopped.
	State string `json:"state"`
	// Activity is what it is doing now, in the engine's words ("Reading
	// go.mod"); LastTool is the tool it last called.
	Activity   string        `json:"activity,omitempty"`
	LastTool   string        `json:"last_tool,omitempty"`
	Tokens     int64         `json:"tokens,omitempty"`
	ToolUses   int           `json:"tool_uses,omitempty"`
	Duration   time.Duration `json:"duration,omitempty"`
	Depth      int           `json:"depth,omitempty"`
	Background bool          `json:"background,omitempty"`
	Started    time.Time     `json:"started,omitempty"`
	Ended      time.Time     `json:"ended,omitempty"`
	// Summary is the last thing it said: while it works, what it is about to
	// do; once done, what it reported back.
	Summary string `json:"summary,omitempty"`
	// Calls are its own tool calls, results cut short; an Agent call among
	// them carries the agent it started.
	Calls []ToolCall `json:"calls,omitempty"`
}

// Running says the agent has not finished.
func (a *SubAgent) Running() bool {
	return a != nil && (a.State == "" || a.State == "starting" || a.State == "running")
}

// SetSubAgent records what a sub-agent is doing on the call that started it.
// It reports whether the call was found.
func (s *Session) SetSubAgent(toolUse string, a SubAgent) bool {
	for i := len(s.Messages) - 1; i >= 0; i-- {
		for j := range s.Messages[i].Tools {
			if s.Messages[i].Tools[j].ID == toolUse {
				cp := a
				s.Messages[i].Tools[j].Agent = &cp
				return true
			}
		}
	}
	return false
}

// Summary renders the tool call as a single compact line for the transcript.
func (t ToolCall) Summary() string {
	var m map[string]any
	if len(t.Input) > 0 && json.Unmarshal(t.Input, &m) != nil {
		// The call is still arriving. Half a path is worth showing: it is the
		// difference between watching the agent work and watching it hang.
		m = partialFields(t.Input)
	}
	pick := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := m[k]; ok {
				if s, ok := v.(string); ok && s != "" {
					return s
				}
			}
		}
		return ""
	}
	// Tool names differ between the built-in agent and the CLIs it drives —
	// read_file and Read, bash and Bash — so they are matched case-insensitively
	// and by both spellings. Without this a CLI's calls fall through and the
	// transcript shows raw JSON instead of the command.
	switch strings.ToLower(t.Name) {
	case "read_file", "read", "notebookread":
		return pick("path", "file_path", "notebook_path")
	case "list_dir", "ls", "glob":
		if p := pick("path", "pattern"); p != "" {
			return p
		}
		return "."
	case "find_files", "grep", "search", "websearch":
		q := pick("query", "pattern")
		if p := pick("path", "dir"); p != "" {
			return q + "  in " + p
		}
		return q
	case "write_file", "edit_file", "write", "edit", "multiedit", "notebookedit":
		return pick("path", "file_path", "notebook_path")
	case "bash", "shell", "command_execution", "bashoutput":
		return firstLine(pick("command", "description"))
	case "task", "agent":
		return pick("description", "prompt")
	case "webfetch", "fetch":
		return pick("url")
	case "task_output", "task_stop", "killshell":
		return pick("task_id", "shell_id")
	case "ask_user":
		if t.Chosen != "" {
			return pick("question") + "  →  " + t.Chosen
		}
		return pick("question")
	}
	if len(t.Input) > 0 && len(t.Input) < 120 {
		return string(t.Input)
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

// Message is one turn. An assistant turn may carry both text and tool calls.
type Message struct {
	Role     string     `json:"role"`
	Text     string     `json:"text,omitempty"`
	Thinking string     `json:"thinking,omitempty"`
	Tools    []ToolCall `json:"tools,omitempty"`
	At       time.Time  `json:"at"`
	Err      string     `json:"err,omitempty"`

	// Shell is set on a message the user produced by running a command with
	// `!` rather than by writing a prompt. The role stays "user", because the
	// agent is meant to see what was run and what it printed — the command is
	// part of the conversation, not a detour from it. Only the rendering
	// differs, and this is what tells the transcript to render it that way.
	Shell *ShellRun `json:"shell,omitempty"`
	// Steered marks a message the user sent while a turn ran, handed to the
	// agent mid-turn rather than as a turn of its own.
	Steered bool `json:"steered,omitempty"`

	// Files are what the user attached to this message: today, images pasted
	// from the clipboard.
	Files []Attachment `json:"files,omitempty"`
}

// Attachment is a file the user put into a message.
//
// The bytes are not stored in the session: a transcript is meant to stay a
// readable JSON file, and a screenshot inlined into it is neither readable nor
// small. What is stored is where the file went, so a reopened session can show
// it again and a resumed turn can send it again.
type Attachment struct {
	// Path is where the file lives on this machine, which is what reads it.
	Path string `json:"path"`
	// Ref is the same file as the session's own filesystem sees it: a session
	// aimed at WSL or a container runs its agent there, and a C:\ path means
	// nothing inside either. Empty when the two are the same, and empty when
	// the file could not be put within reach at all.
	Ref string `json:"ref,omitempty"`
	// FS is the filesystem Ref is a path in. A session can be repointed
	// between pasting an image and sending it, and a path that was right for
	// the container it was copied into is wrong everywhere else.
	FS    string `json:"fs,omitempty"`
	Media string `json:"media,omitempty"`
	Bytes int64  `json:"bytes,omitempty"`
}

// Where is the path to hand an engine that can only open files for itself.
func (a Attachment) Where() string {
	if a.Ref != "" {
		return a.Ref
	}
	return a.Path
}

// ShellRun is a command the user ran with `!`, and what it printed.
type ShellRun struct {
	Command string `json:"command"`
	// Where is the filesystem it ran in, for the header: a session that moves
	// between the host and WSL leaves a transcript where that matters.
	Where  string `json:"where,omitempty"`
	Dir    string `json:"dir,omitempty"`
	Output string `json:"output,omitempty"`
	Exit   int    `json:"exit"`
	// Started is when it was launched, for the clock while it runs; Elapsed is
	// what that clock stopped at, which is what a reopened session shows.
	Started time.Time     `json:"-"`
	Elapsed time.Duration `json:"elapsed,omitempty"`
	Done    bool          `json:"done,omitempty"`
}

// Session is a single conversation thread.
// EngineState is what one engine left behind in this session.
type EngineState struct {
	// ExternalID is that engine's own session id, so coming back to it resumes
	// its conversation instead of starting a second one beside it.
	ExternalID string `json:"external_id,omitempty"`
	// Seen is how many messages this transcript had when that engine last
	// finished a turn. Everything after it happened while another engine held
	// the session, and it is exactly what a handoff has to catch it up on.
	//
	// It is why an id on its own is not enough. Resuming an engine after a
	// detour gives you one that remembers the first half of the conversation,
	// has never heard of the second, and will not say so.
	Seen int `json:"seen,omitempty"`
}

// StateFor is what engine remembers about this session.
func (s *Session) StateFor(engine string) EngineState { return s.Engines[engine] }

// SetExternalID records an engine's own session id.
//
// It is keyed by the engine that produced it rather than by the session's
// current engine: a turn can finish after the session has been handed on, and
// the id belongs to whoever made it.
func (s *Session) SetExternalID(engine, id string) {
	if engine == "" || id == "" {
		return
	}
	st := s.engineSlot(engine)
	st.ExternalID = id
	s.Engines[engine] = st
	if engine == s.Engine {
		s.ExternalID = id
	}
	s.Dirty = true
}

// ConversationID identifies the context, not a turn or an engine subprocess.
func (s *Session) ConversationID() string {
	return s.ID + ":" + strconv.FormatUint(s.ConversationGeneration, 10)
}

// Fresh starts the agent's context at the next message: the next turn is
// given nothing before it, and no engine resumes the conversation it had.
func (s *Session) Fresh() {
	s.ConversationGeneration++
	s.ContextFrom = len(s.Messages)
	s.Engines, s.ExternalID = nil, ""
	s.Dirty = true
}

// Context is the part of the transcript an engine is given.
func (s *Session) Context() []Message {
	return s.Messages[min(max(s.ContextFrom, 0), len(s.Messages)):]
}

// SeenBy is where an engine's catching up begins: after what it was last
// given, and never before the context does.
func (s *Session) SeenBy(engine string) int {
	return max(s.StateFor(engine).Seen, s.ContextFrom)
}

// SetSeen records how much of the transcript an engine has been given.
func (s *Session) SetSeen(engine string, n int) {
	if engine == "" {
		return
	}
	st := s.engineSlot(engine)
	if n > st.Seen {
		st.Seen = n
		s.Engines[engine] = st
		s.Dirty = true
	}
}

// ForgetEngines drops every engine's server-side conversation.
//
// For when the ground moved under all of them at once — a different filesystem
// is a different project, and an id that resumes a conversation about other
// files is worse than no id at all.
func (s *Session) ForgetEngines() {
	s.ConversationGeneration++
	s.Engines, s.ExternalID = nil, ""
	s.Dirty = true
}

func (s *Session) engineSlot(engine string) EngineState {
	if s.Engines == nil {
		s.Engines = make(map[string]EngineState, 2)
	}
	return s.Engines[engine]
}

// normalise fills in what an older session file does not carry.
//
// A file written before engines were tracked separately has one id, and it
// belongs to whichever engine was selected when it was written — nothing else
// could have produced it. Seen is the whole transcript, because that engine ran
// all of it. Nothing is written back: the file is migrated the next time it is
// saved anyway, and an id read into the wrong slot would resume the wrong
// conversation, which is worse than a cold start.
func (s *Session) normalise() {
	if s.CWD == "" {
		s.CWD = s.Root
	}
	if s.Engines == nil && s.Engine != "" && s.ExternalID != "" {
		s.Engines = map[string]EngineState{
			s.Engine: {ExternalID: s.ExternalID, Seen: len(s.Messages)},
		}
	}
}

type Session struct {
	// ConversationGeneration changes when this session starts a fresh context,
	// so recurring scheduled runs rotate without creating extra session rows.
	ConversationGeneration uint64 `json:"conversation_generation,omitempty"`

	ID    string `json:"id"`
	Title string `json:"title"`
	Root  string `json:"root"`
	Model string `json:"model"`

	// Engine is the backend that runs this session's turns: "api" for the
	// built-in Anthropic client, or the id of an external CLI.
	Engine string `json:"engine,omitempty"`
	// ExternalID is the CLI's own session identifier for whichever engine is
	// selected now. Engines below is the same thing for every engine that has
	// ever run here; this one is kept because sessions written before that
	// existed carry only it.
	ExternalID string `json:"external_id,omitempty"`
	// Engines is what each engine remembers about this session, by engine id.
	//
	// It is not a property of the conversation but of an engine's turn at
	// holding it, which is why it could not stay a single field: switching
	// engine used to destroy the id, so coming back started cold a second
	// time. An engine's own conversation lives on its own server and cannot be
	// read, merged or handed over — only resumed — so the one thing this can
	// do is remember where to resume from.
	Engines map[string]EngineState `json:"engines,omitempty"`
	// ForkPending marks a session that was branched off another one but has
	// not run a turn yet. The next turn forks the engine's own conversation, so
	// the context carries over without disturbing the session it came from.
	ForkPending bool `json:"fork_pending,omitempty"`
	// SideOf is the session this one is a side chat of, if it is one. A side
	// chat is a real session — it has its own history, its own engine, and it
	// runs at the same time — but it belongs to the conversation it was asked
	// beside rather than standing on its own, so it is shown in that
	// conversation's pane instead of in the list of them.
	SideOf string `json:"side_of,omitempty"`
	// Job is the scheduled job this session is a run of, if it is one. A run
	// is not a conversation someone had: it does not set the defaults of the
	// next one, and terminals are not handed it.
	Job string `json:"job,omitempty"`
	// SideFrom is how much of the parent it inherited. The agent is given all
	// of it — that is what makes the aside worth asking — but the reader has
	// it already, in the pane next to this one, so the pane shows what was
	// said after this point and nothing before it.
	SideFrom int `json:"side_from,omitempty"`
	// ContextFrom is where the agent's context begins. What comes before it
	// stays for the reader but is given to no engine. The runs of a
	// scheduled job share one session so they do not crowd the list, and
	// each starts afresh (Fresh) rather than carrying every earlier run.
	ContextFrom int `json:"context_from,omitempty"`
	// CWD is the directory this session's agent runs in. It defaults to the
	// project root but can be narrowed to a subdirectory, and the file tree
	// always shows whatever it points at.
	CWD string `json:"cwd,omitempty"`
	// Target is the filesystem this session works in: "host",
	// "docker:<container>", or "wsl:<distribution>". The explorer, the preview,
	// search and the agent all follow it.
	Target string `json:"target,omitempty"`
	// Mode is how much this session's agent may do without asking. Empty
	// means auto, which is what a new session gets.
	Mode string `json:"mode,omitempty"`
	// Closed takes a conversation out of the list without deleting it: the
	// next start does not restore it, and /recall still finds it.
	Closed bool `json:"closed,omitempty"`
	// Pinned keeps a conversation at the top of the web's list.
	Pinned bool `json:"pinned,omitempty"`
	// Chapters are the messages pinned as chapters, by index, in order: the
	// places in a long conversation worth jumping back to.
	Chapters []int     `json:"chapters,omitempty"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`

	Messages []Message `json:"messages"`

	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	CacheReads   int64 `json:"cache_reads"`

	// Runtime-only state, never persisted.
	Busy   bool   `json:"-"`
	Status string `json:"-"`
	// Started is when this turn began. A turn that has been thinking for seven
	// minutes and one that has been thinking for seven seconds look identical
	// otherwise, and only one of them is worth interrupting.
	Started time.Time `json:"-"`
	// Running counts the `!` commands this session has in flight. They do not
	// make it busy — you can go on asking while one runs — but their clocks
	// still have to be redrawn, and this is what says there is one to redraw.
	Running int `json:"-"`
	// Unseen marks a conversation whose turn finished while the reader was
	// looking at a different one. It is the state that makes a list of
	// conversations worth having — the answer arrived somewhere you were not —
	// and it is runtime only: what it records is whether you have looked since
	// it happened, and a new process has not.
	Unseen bool `json:"-"`
	// RunAt is when the tool now running started. The finished ones say how
	// long they took; the one you are waiting on is the one you want it from.
	RunAt   time.Time `json:"-"`
	Partial string    `json:"-"` // streaming text not yet committed to Messages
	LastErr string    `json:"-"`
	Dirty   bool      `json:"-"`
	// Calls are the tool calls of the turn being written right now, while the
	// model is still emitting them. They are shown so that a long write or a
	// slow search is something you watch rather than something you wait for,
	// and they are dropped the moment the finished turn arrives with the same
	// calls in it.
	Calls []ToolCall `json:"-"`
	// Output is what the tool running right now has printed so far, and the
	// call it belongs to. A build or a test run is the whole reason to watch a
	// turn at all, and it says nothing until it exits unless it is forwarded.
	// One tool runs at a time, so there is one of these rather than one per
	// call, and it is dropped as soon as that call finishes.
	Output   string `json:"-"`
	OutputID string `json:"-"`
	// What the turn is doing, said the way a person watching wants to know
	// it: since when (PhaseAt, reset whenever Status changes), when the engine
	// last said anything at all (HeardAt — a long silence is how a stall
	// looks), how much it has streamed (Streamed, in bytes of text, thinking
	// and tool input), and the thinking it is doing right now (Thinking, the
	// tail of it, dropped once it starts answering).
	PhaseAt  time.Time `json:"-"`
	HeardAt  time.Time `json:"-"`
	Streamed int       `json:"-"`
	Thinking string    `json:"-"`
	ThinkTok int       `json:"-"`
	// Queued are prompts sent while a turn was still running, in order. They
	// go, one turn each, as the turns before them end.
	Queued []Queued `json:"-"`
	// Live holds the SDK-native message history for an in-flight conversation.
	// It is opaque here on purpose: saved sessions never depend on the SDK's wire
	// types, while a running session can still replay thinking blocks verbatim,
	// which the API requires when tool results follow a thinking turn.
	Live any `json:"-"`
}

// Append adds a message and refreshes the derived title.
func (s *Session) Append(m Message) {
	if m.At.IsZero() {
		m.At = time.Now()
	}
	s.Messages = append(s.Messages, m)
	s.Updated = m.At
	s.Dirty = true
	// A command run with `!` is not what the session is about, so it does not
	// get to name it.
	if s.Title == "" && m.Role == RoleUser && m.Shell == nil {
		s.Title = deriveTitle(m.Text)
	}
}

// Last returns a pointer to the final message, or nil when empty.
func (s *Session) Last() *Message {
	if len(s.Messages) == 0 {
		return nil
	}
	return &s.Messages[len(s.Messages)-1]
}

// Label is what the session tab shows.
func (s *Session) Label() string {
	if s.Title != "" {
		return s.Title
	}
	return "new session"
}

func deriveTitle(text string) string {
	t := strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if t == "" {
		return ""
	}
	// Long enough to fill the two lines the session list wraps a title onto:
	// what distinguishes two conversations is often near the end of the
	// sentence, not the start.
	//
	// The limit is in columns rather than runes, because that is what the list
	// has: a title in Japanese is twice as wide as one of the same length in
	// English, and counting runes would give it twice the room.
	const maxTitle = 72
	if ansi.StringWidth(t) <= maxTitle {
		return t
	}
	return strings.TrimSpace(ansi.Truncate(t, maxTitle, "")) + "…"
}

// pathKeys are the input fields that name a file, across the tools the built-in
// agent and the CLIs expose. Their spellings differ; the meaning does not.
var pathKeys = []string{
	"file_path", "path", "notebook_path", "filePath", "target_file", "new_path",
}

// WrittenPaths are the files a call that writes names, as it named them —
// relative to the agent's directory or absolute. Nil for every other call,
// including a shell command, which may well write files but does not say which.
func (t ToolCall) WrittenPaths() []string {
	switch strings.ToLower(t.Name) {
	case "write_file", "edit_file", "write", "edit", "multiedit", "notebookedit":
	default:
		return nil
	}
	var m map[string]any
	if len(t.Input) == 0 || json.Unmarshal(t.Input, &m) != nil {
		return nil
	}
	return collectPaths(m, 0)
}

// PathsInside reports whether every path this call names lies inside root.
//
// decided is false when the call names no path at all — a shell command, say —
// because there is nothing to judge and guessing would be worse than asking.
//
// With more than one root a path need only lie inside one of them.
func (t ToolCall) PathsInside(roots ...string) (inside, decided bool) {
	if len(roots) == 0 || roots[0] == "" || len(t.Input) == 0 {
		return false, false
	}
	var m map[string]any
	if json.Unmarshal(t.Input, &m) != nil {
		return false, false
	}
	paths := collectPaths(m, 0)
	if len(paths) == 0 {
		return false, false
	}
	for _, p := range paths {
		if !withinAny(roots, p) {
			return false, true
		}
	}
	return true, true
}

// withinAny says p lies inside one of roots. A relative path is relative to
// the first, the project, and only an absolute one is weighed against the
// others.
func withinAny(roots []string, p string) bool {
	if vfs.Within(roots[0], p) {
		return true
	}
	if !vfs.IsAbs(p) {
		return false
	}
	for _, r := range roots[1:] {
		if r != "" && vfs.Within(r, p) {
			return true
		}
	}
	return false
}

// collectPaths gathers path-shaped values, descending into the nested edit
// lists that some tools take.
func collectPaths(m map[string]any, depth int) []string {
	if depth > 3 {
		return nil
	}
	var out []string
	for _, key := range pathKeys {
		if v, ok := m[key].(string); ok && v != "" {
			out = append(out, v)
		}
	}
	for _, v := range m {
		switch child := v.(type) {
		case map[string]any:
			out = append(out, collectPaths(child, depth+1)...)
		case []any:
			for _, item := range child {
				if sub, ok := item.(map[string]any); ok {
					out = append(out, collectPaths(sub, depth+1)...)
				}
			}
		}
	}
	return out
}

// Queued is a prompt waiting for the turn before it to end, with the images
// that were attached when it was sent.
type Queued struct {
	Text  string
	Files []Attachment
}

// MarkTool records a finished call on the message that made it, and reports
// which message that was (-1 if none).
//
// It merges rather than replaces: an engine's completion event often carries
// only the id and the result, because the name and arguments were already sent
// with the call. Overwriting would blank them out of the transcript.
func (s *Session) MarkTool(call ToolCall) int {
	for i := len(s.Messages) - 1; i >= 0; i-- {
		for j := range s.Messages[i].Tools {
			cur := &s.Messages[i].Tools[j]
			if cur.ID != call.ID {
				continue
			}
			if call.Name != "" {
				cur.Name = call.Name
			}
			if len(call.Input) > 0 {
				cur.Input = call.Input
			}
			cur.Result, cur.IsError, cur.Done = call.Result, call.IsError, call.Done
			if call.Denied {
				cur.Denied = true
			}
			if call.Elapsed > 0 {
				cur.Elapsed = call.Elapsed
			}
			if call.Agent != nil {
				cur.Agent = call.Agent
			}
			return i
		}
	}
	return -1
}
