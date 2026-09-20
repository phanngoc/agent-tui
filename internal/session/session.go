// Package session models a conversation and owns its on-disk persistence.
// It deliberately knows nothing about the Anthropic SDK: the agent converts
// these plain structs into API params, which keeps saved sessions readable and
// independent of any SDK version.
package session

import (
	"encoding/json"
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
type Session struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Root  string `json:"root"`
	Model string `json:"model"`

	// Engine is the backend that runs this session's turns: "api" for the
	// built-in Anthropic client, or the id of an external CLI.
	Engine string `json:"engine,omitempty"`
	// ExternalID is the CLI's own session identifier, kept so a restored
	// session can resume the CLI's conversation instead of starting over.
	ExternalID string `json:"external_id,omitempty"`
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
	// SideFrom is how much of the parent it inherited. The agent is given all
	// of it — that is what makes the aside worth asking — but the reader has
	// it already, in the pane next to this one, so the pane shows what was
	// said after this point and nothing before it.
	SideFrom int `json:"side_from,omitempty"`
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
	Mode    string    `json:"mode,omitempty"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`

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

// PathsInside reports whether every path this call names lies inside root.
//
// decided is false when the call names no path at all — a shell command, say —
// because there is nothing to judge and guessing would be worse than asking.
func (t ToolCall) PathsInside(root string) (inside, decided bool) {
	if root == "" || len(t.Input) == 0 {
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
		if !vfs.Within(root, p) {
			return false, true
		}
	}
	return true, true
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
