// Package session models a conversation and owns its on-disk persistence.
// It deliberately knows nothing about the Anthropic SDK: the agent converts
// these plain structs into API params, which keeps saved sessions readable and
// independent of any SDK version.
package session

import (
	"encoding/json"
	"strings"
	"time"

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
	if len(t.Input) > 0 {
		_ = json.Unmarshal(t.Input, &m)
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
	// CWD is the directory this session's agent runs in. It defaults to the
	// project root but can be narrowed to a subdirectory, and the file tree
	// always shows whatever it points at.
	CWD string `json:"cwd,omitempty"`
	// Target is the filesystem this session works in: "host", or
	// "docker:<container>". The explorer, the preview, search and the agent all
	// follow it.
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
	Busy    bool   `json:"-"`
	Status  string `json:"-"`
	Partial string `json:"-"` // streaming text not yet committed to Messages
	LastErr string `json:"-"`
	Dirty   bool   `json:"-"`
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
	if s.Title == "" && m.Role == RoleUser {
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
	const maxTitle = 42
	r := []rune(t)
	if len(r) > maxTitle {
		return strings.TrimSpace(string(r[:maxTitle])) + "…"
	}
	return t
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
