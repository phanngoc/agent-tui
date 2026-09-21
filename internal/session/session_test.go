package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTitleDerivedFromFirstUserMessage(t *testing.T) {
	s := &Session{}
	if s.Label() != "new session" {
		t.Errorf("empty session label = %q", s.Label())
	}
	s.Append(Message{Role: RoleUser, Text: "fix the ranking in search\nplease"})
	if s.Title != "fix the ranking in search please" {
		t.Errorf("title = %q", s.Title)
	}
	s.Append(Message{Role: RoleUser, Text: "and then this"})
	if s.Title != "fix the ranking in search please" {
		t.Error("title should be set once, from the first message")
	}
}

func TestTitleTruncatesOnRuneBoundaries(t *testing.T) {
	s := &Session{}
	s.Append(Message{Role: RoleUser, Text: "sửa lỗi tìm kiếm trong dự án này cho tôi với nhé bạn ơi"})
	// maxTitle, plus the ellipsis it appends.
	if len([]rune(s.Title)) > 73 {
		t.Errorf("title too long: %q", s.Title)
	}
	if !json.Valid([]byte(`"` + s.Title + `"`)) {
		t.Errorf("title is not valid UTF-8: %q", s.Title)
	}
}

func TestToolSummary(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"read_file", `{"path":"internal/ui/view.go"}`, "internal/ui/view.go"},
		{"grep", `{"query":"needle","path":"internal"}`, "needle  in internal"},
		{"bash", `{"command":"go test ./...\nsecond line"}`, "go test ./... …"},
		{"list_dir", `{}`, "."},
	}
	for _, c := range cases {
		tc := ToolCall{Name: c.name, Input: json.RawMessage(c.input)}
		if got := tc.Summary(); got != c.want {
			t.Errorf("%s summary = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestManagerRoundTrip(t *testing.T) {
	dir, root := t.TempDir(), "/project/x"

	m := NewManager(dir, root, "claude-opus-5")
	t.Cleanup(m.Shutdown)
	m.Restore(10)
	if m.Len() != 1 {
		t.Fatalf("a fresh project should start with one session, got %d", m.Len())
	}

	s := m.Active()
	s.Append(Message{Role: RoleUser, Text: "remember me"})
	s.Append(Message{Role: RoleAssistant, Text: "ok", Tools: []ToolCall{
		{ID: "t1", Name: "read_file", Input: json.RawMessage(`{"path":"a.go"}`), Result: "ok", Done: true},
	}})
	m.SaveAll()

	// A second manager over the same directory must see the saved session.
	m2 := NewManager(dir, root, "claude-opus-5")
	t.Cleanup(m2.Shutdown)
	m2.Restore(10)
	if m2.Len() != 1 {
		t.Fatalf("restored %d sessions, want 1", m2.Len())
	}
	got := m2.Active()
	if got.Title != "remember me" || len(got.Messages) != 2 {
		t.Fatalf("restored session = %+v", got)
	}
	if len(got.Messages[1].Tools) != 1 || got.Messages[1].Tools[0].Name != "read_file" {
		t.Errorf("tool calls did not survive the round trip: %+v", got.Messages[1].Tools)
	}
}

func TestManagerIgnoresOtherProjects(t *testing.T) {
	dir := t.TempDir()

	a := NewManager(dir, "/project/a", "m")
	t.Cleanup(a.Shutdown)
	a.Restore(10)
	a.Active().Append(Message{Role: RoleUser, Text: "in project a"})
	a.SaveAll()

	b := NewManager(dir, "/project/b", "m")
	t.Cleanup(b.Shutdown)
	b.Restore(10)
	if b.Len() != 1 || len(b.Active().Messages) != 0 {
		t.Errorf("project b picked up project a's history: %+v", b.Active())
	}
}

func TestCycleAndSelectWrapAround(t *testing.T) {
	m := NewManager(t.TempDir(), "/r", "m")
	t.Cleanup(m.Shutdown)
	m.Restore(10)
	m.New()
	m.New() // three sessions, active = 0

	m.Cycle(-1)
	if got := m.ActiveIndex(); got != 2 {
		t.Errorf("cycling back from 0 = %d, want 2", got)
	}
	m.Cycle(1)
	if got := m.ActiveIndex(); got != 0 {
		t.Errorf("cycling forward from 2 = %d, want 0", got)
	}
	m.Select(99) // out of range is ignored rather than panicking
	if m.ActiveIndex() != 0 {
		t.Error("an out-of-range Select changed the active session")
	}
}

func TestCloseLastSessionLeavesAFreshOne(t *testing.T) {
	m := NewManager(t.TempDir(), "/r", "m")
	t.Cleanup(m.Shutdown)
	m.Restore(10)
	m.Close(0)
	if m.Len() != 1 {
		t.Fatalf("closing the only session left %d behind", m.Len())
	}
	if len(m.Active().Messages) != 0 {
		t.Error("the replacement session should be empty")
	}
}

func TestCloneDetachesRuntimeState(t *testing.T) {
	s := &Session{ID: "x", Busy: true, Partial: "streaming…", Updated: time.Now()}
	s.Append(Message{Role: RoleUser, Text: "hi"})

	c := s.clone()
	if c.Busy || c.Partial != "" {
		t.Error("runtime state leaked into the persisted copy")
	}
	c.Messages[0].Text = "mutated"
	if s.Messages[0].Text != "hi" {
		t.Error("clone shares the message backing array with the live session")
	}
}

// TestSaveDoesNotRaceWithToolUpdates reproduces a real data race: a tool call is
// filled in after its message is already in the transcript, so a save that
// shallow-copied the messages left the writer marshalling the same ToolCall
// slice the UI was still writing to.
func TestSaveDoesNotRaceWithToolUpdates(t *testing.T) {
	m := NewManager(t.TempDir(), "/r", "model")
	t.Cleanup(m.Shutdown)
	m.Restore(1)
	s := m.Active()
	s.Append(Message{Role: RoleUser, Text: "go"})

	// Enough payload that a marshal is still running when the next mutation
	// lands; without that the window closes before the race can be observed.
	tools := make([]ToolCall, 0, 64)
	for i := 0; i < 64; i++ {
		tools = append(tools, ToolCall{
			ID:    "t" + strconv.Itoa(i),
			Name:  "bash",
			Input: json.RawMessage(`{"command":"` + strings.Repeat("x", 512) + `"}`),
		})
	}
	s.Append(Message{Role: RoleAssistant, Tools: tools})

	// This is the production shape: the UI goroutine mutates and saves, and the
	// writer goroutine marshals whatever it was handed. The snapshot has to be
	// deep enough that the next mutation cannot reach it.
	for i := 0; i < 400; i++ {
		m.Save(s)
		for j := range s.Messages[1].Tools {
			c := &s.Messages[1].Tools[j]
			c.Done, c.IsError = true, i%2 == 0
			c.Result = strings.Repeat("r", 256)
			c.Elapsed = time.Duration(i) * time.Millisecond
		}
		runtime.Gosched()
	}
	m.SaveAll()
}

func TestCloneDeepCopiesToolCalls(t *testing.T) {
	s := &Session{ID: "x"}
	s.Append(Message{Role: RoleAssistant, Tools: []ToolCall{{ID: "t1", Name: "bash"}}})

	c := s.clone()
	c.Messages[0].Tools[0].Name = "mutated"
	if s.Messages[0].Tools[0].Name != "bash" {
		t.Error("the clone shares its tool calls with the live session")
	}
}

func TestForkCopiesTheTranscript(t *testing.T) {
	m := NewManager(t.TempDir(), "/r", "model")
	t.Cleanup(m.Shutdown)
	m.Restore(1)

	src := m.Active()
	src.Engine, src.CWD, src.Target = "claude", "/r/sub", "docker:demo"
	src.ExternalID = "sess-123"
	src.Append(Message{Role: RoleUser, Text: "the original question"})
	src.Append(Message{Role: RoleAssistant, Text: "an answer", Tools: []ToolCall{
		{ID: "t1", Name: "bash", Result: "ok", Done: true},
	}})

	f := m.Fork(src)

	if f == src || f.ID == src.ID {
		t.Fatal("fork returned the same session")
	}
	if len(f.Messages) != len(src.Messages) {
		t.Fatalf("fork has %d messages, want %d", len(f.Messages), len(src.Messages))
	}
	for _, want := range []struct{ name, got, exp string }{
		{"engine", f.Engine, src.Engine},
		{"cwd", f.CWD, src.CWD},
		{"target", f.Target, src.Target},
		{"external id", f.ExternalID, src.ExternalID},
	} {
		if want.got != want.exp {
			t.Errorf("fork %s = %q, want %q", want.name, want.got, want.exp)
		}
	}
	if !f.ForkPending {
		t.Error("a fork of a session with engine history should branch on its next turn")
	}
	if !strings.Contains(f.Title, "fork") {
		t.Errorf("fork title = %q, want it to say so", f.Title)
	}

	// The source must be untouched, and the copy must be independent.
	f.Messages[0].Text = "changed"
	f.Messages[1].Tools[0].Result = "changed"
	if src.Messages[0].Text != "the original question" {
		t.Error("editing the fork changed the original's text")
	}
	if src.Messages[1].Tools[0].Result != "ok" {
		t.Error("editing the fork changed the original's tool calls")
	}
	if src.ForkPending {
		t.Error("forking marked the source as pending")
	}
}

func TestForkOfALocalSessionHasNothingToBranch(t *testing.T) {
	m := NewManager(t.TempDir(), "/r", "model")
	t.Cleanup(m.Shutdown)
	m.Restore(1)

	src := m.Active()
	src.Append(Message{Role: RoleUser, Text: "hello"}) // no ExternalID

	f := m.Fork(src)
	if f.ForkPending {
		t.Error("there is no engine conversation to branch, so nothing should be pending")
	}
	if len(f.Messages) != 1 {
		t.Errorf("the transcript should still be copied: %+v", f.Messages)
	}
}

func TestForkTitlesCount(t *testing.T) {
	for in, want := range map[string]string{
		"fix ranking":           "fix ranking (fork)",
		"fix ranking (fork)":    "fix ranking (fork 2)",
		"fix ranking (fork 2)":  "fix ranking (fork 3)",
		"fix ranking (fork 12)": "fix ranking (fork 13)",
	} {
		if got := forkTitle(in); got != want {
			t.Errorf("forkTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestToolSummaryHandlesCLIToolNames guards a display bug: the built-in agent
// calls it bash and Claude Code calls it Bash, so a case-sensitive match left
// the transcript showing raw JSON instead of the command.
func TestToolSummaryHandlesCLIToolNames(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
	}{
		{"bash", `{"command":"go test ./..."}`, "go test ./..."},
		{"Bash", `{"command":"go test ./..."}`, "go test ./..."},
		{"Read", `{"file_path":"internal/ui/view.go"}`, "internal/ui/view.go"},
		{"read_file", `{"path":"internal/ui/view.go"}`, "internal/ui/view.go"},
		{"Edit", `{"file_path":"a.go"}`, "a.go"},
		{"Write", `{"file_path":"b.go"}`, "b.go"},
		{"Glob", `{"pattern":"**/*.go"}`, "**/*.go"},
		{"WebFetch", `{"url":"https://example.com"}`, "https://example.com"},
		{"Task", `{"description":"review the diff"}`, "review the diff"},
	} {
		got := ToolCall{Name: tc.name, Input: json.RawMessage(tc.input)}.Summary()
		if got != tc.want {
			t.Errorf("%s summary = %q, want %q", tc.name, got, tc.want)
		}
		if strings.HasPrefix(got, "{") {
			t.Errorf("%s fell through to raw JSON: %q", tc.name, got)
		}
	}
}

// A file written before engines were tracked separately has one id, and it
// belongs to whichever engine was selected when it was written — nothing else
// could have produced it.
func TestOldSessionFileMigratesItsExternalID(t *testing.T) {
	dir := t.TempDir()
	old := `{"id":"s1","title":"t","root":"/p","engine":"claude",` +
		`"external_id":"claude-1","created":"2026-01-01T00:00:00Z",` +
		`"updated":"2026-01-01T00:00:00Z",` +
		`"messages":[{"role":"user","text":"a","at":"2026-01-01T00:00:00Z"},` +
		`{"role":"assistant","text":"b","at":"2026-01-01T00:00:00Z"}]}`
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sessions", "s1.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}

	m := NewManager(dir, "/p", "model")
	t.Cleanup(m.Shutdown)
	m.Restore(5)

	s := m.Active()
	if got := s.StateFor("claude"); got.ExternalID != "claude-1" {
		t.Errorf("claude's id did not migrate: %+v", got)
	} else if got.Seen != len(s.Messages) {
		t.Errorf("claude saw %d of %d messages; it ran all of them", got.Seen, len(s.Messages))
	}
	// Nothing else may claim it: an id read into the wrong slot resumes the
	// wrong conversation, which is worse than a cold start.
	if got := s.StateFor("codex").ExternalID; got != "" {
		t.Errorf("codex was handed claude's id: %q", got)
	}
	// And the default the old field carried is still applied.
	if s.CWD != "/p" {
		t.Errorf("cwd = %q, want the root", s.CWD)
	}
}

// Forking carries only the engine that was running. Another engine's id still
// points at the parent's own conversation, and resuming it without a fork flag
// would write this session's turns into the one it came from.
func TestForkDoesNotInheritOtherEnginesIDs(t *testing.T) {
	m := NewManager(t.TempDir(), "/p", "model")
	t.Cleanup(m.Shutdown)
	parent := m.New()
	parent.Engine = "claude"
	parent.SetExternalID("claude", "claude-1")
	parent.SetExternalID("codex", "codex-1")
	parent.ExternalID = "claude-1"
	parent.Append(Message{Role: RoleUser, Text: "hi"})

	child := m.Fork(parent)

	if got := child.StateFor("claude").ExternalID; got != "claude-1" {
		t.Errorf("the branch lost the engine it was running: %q", got)
	}
	if got := child.StateFor("codex").ExternalID; got != "" {
		t.Errorf("the branch inherited codex's id from its parent: %q", got)
	}
}

// Whatever is active is something the session list shows.
//
// An aside is deliberately absent from every list of conversations, so a
// cursor resting on one points at a session nothing draws: the transcript
// fills with it and says what it is doing, while every row of the list says
// idle. A screen that contradicts itself, from one integer.
func TestTheActiveSessionIsAlwaysOneTheListShows(t *testing.T) {
	m := NewManager(t.TempDir(), "/p", "model")
	t.Cleanup(m.Shutdown)

	parent := m.New()
	parent.Append(Message{Role: RoleUser, Text: "hỏi"})
	aside := m.Fork(parent)
	aside.SideOf, aside.SideFrom = parent.ID, len(aside.Messages)
	other := m.New()
	other.Append(Message{Role: RoleUser, Text: "khác"})

	at := func() int {
		for i, s := range m.All() {
			if s == aside {
				return i
			}
		}
		return -1
	}

	// Selecting one directly does not take.
	m.Select(at())
	if m.Active() == aside {
		t.Error("Select moved the cursor onto an aside")
	}

	// Nor does closing whatever happened to sit beside it.
	for m.Len() > 1 {
		before := m.Len()
		m.Close(m.ActiveIndex())
		if m.Active().SideOf != "" {
			t.Fatalf("closing left the cursor on an aside (%d sessions before)", before)
		}
	}
}
