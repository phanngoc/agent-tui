package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// store writes session files straight to disk, the way a previous run left
// them, and returns a manager over that directory.
func store(t *testing.T, root string, docs ...*Session) *Manager {
	t.Helper()
	dir := t.TempDir()
	sub := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, d := range docs {
		b, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, d.ID+".json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m := NewManager(dir, root, "model")
	t.Cleanup(m.Shutdown)
	return m
}

func saved(id, root, text string) *Session {
	return &Session{
		ID: id, Title: text, Root: root, Engine: "api",
		Created: time.Now(), Updated: time.Now(),
		Messages: []Message{{Role: RoleUser, Text: text, At: time.Now()}},
	}
}

func ids(entries []Entry) string {
	var out []string
	for _, e := range entries {
		out = append(out, e.ID)
	}
	return strings.Join(out, ",")
}

// The requirement, in one assertion: every conversation on disk, whatever
// project it belongs to. Restore cannot do this — it discards anything whose
// root is not the one being opened.
func TestDiskEntriesReadsEveryProject(t *testing.T) {
	m := store(t, "/here",
		saved("a", "/here", "về resolvePfid"),
		saved("b", "/elsewhere", "về ranking"),
		saved("c", "/third", "về deploy"),
	)

	got := m.DiskEntries(context.Background(), nil)
	if len(got) != 3 {
		t.Fatalf("read %d conversations (%s), want all three", len(got), ids(got))
	}
}

// The .tmp files an interrupted save leaves behind are not conversations.
func TestDiskEntriesSkipsPartialWrites(t *testing.T) {
	m := store(t, "/here", saved("a", "/here", "real"))
	tmp := filepath.Join(t.TempDir(), "x")
	_ = tmp
	dir := m.dir
	if err := os.WriteFile(filepath.Join(dir, "b.json.tmp"), []byte(`{"id":"b"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := m.DiskEntries(context.Background(), nil); len(got) != 1 {
		t.Errorf("read %d conversations (%s), want only the finished one", len(got), ids(got))
	}
}

// One unreadable file may not blind the search.
func TestDiskEntriesSurvivesABadFile(t *testing.T) {
	m := store(t, "/here", saved("a", "/here", "real"), saved("c", "/here", "also real"))
	if err := os.WriteFile(filepath.Join(m.dir, "b.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := m.DiskEntries(context.Background(), nil); len(got) != 2 {
		t.Errorf("read %d conversations (%s), want the two that parse", len(got), ids(got))
	}
}

// scanDoc's tags have to track Session's. A rename on one side and not the
// other yields no hits rather than an error, which is the kind of breakage
// nothing notices.
func TestScanDocTracksTheSessionItReads(t *testing.T) {
	full := &Session{
		ID: "s", Title: "t", Root: "/p", SideOf: "parent", Updated: time.Now(),
		Messages: []Message{{Role: RoleAssistant, Text: "cái cần tìm"}},
	}
	b, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	var d scanDoc
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	switch {
	case d.ID != full.ID:
		t.Errorf("id did not survive: %q", d.ID)
	case d.Root != full.Root:
		t.Errorf("root did not survive: %q", d.Root)
	case d.SideOf != full.SideOf:
		t.Errorf("side_of did not survive: %q", d.SideOf)
	case len(d.Messages) != 1 || d.Messages[0].Text != "cái cần tìm":
		t.Errorf("the prose did not survive: %+v", d.Messages)
	case d.Messages[0].Role != RoleAssistant:
		t.Errorf("the role did not survive: %q", d.Messages[0].Role)
	}
}

// A side chat belongs to the conversation it hangs off and is not listed
// anywhere, so a hit that opened one would be a dead end.
func TestDiskEntriesLeavesOutSideChats(t *testing.T) {
	aside := saved("b", "/here", "aside")
	aside.SideOf = "a"
	m := store(t, "/here", saved("a", "/here", "main"), aside)

	if got := m.DiskEntries(context.Background(), nil); len(got) != 1 || got[0].ID != "a" {
		t.Errorf("read %s, want only the conversation it hangs off", ids(got))
	}
}

// The live copy may hold messages the disk has never seen, so it wins.
func TestLiveEntriesSeeUnsavedMessages(t *testing.T) {
	m := store(t, "/here", saved("a", "/here", "on disk"))
	m.Restore(5)
	m.Active().Append(Message{Role: RoleUser, Text: "never saved"})

	live := m.LiveEntries()
	if len(live) != 1 {
		t.Fatalf("snapshotted %d open conversations", len(live))
	}
	if !live[0].Live {
		t.Error("a live entry is not marked as one")
	}
	if got := strings.Join(live[0].Texts, "|"); !strings.Contains(got, "never saved") {
		t.Errorf("the unsaved message is missing: %q", got)
	}
	// And the disk read skips it, so it is not found twice.
	skip := map[string]bool{"a": true}
	if got := m.DiskEntries(context.Background(), skip); len(got) != 0 {
		t.Errorf("the open conversation was read again from disk: %s", ids(got))
	}
}

// The index is the address the transcript is scrolled to, so a message with no
// text still occupies its place.
func TestEntryKeepsEmptyMessagesInPlace(t *testing.T) {
	s := saved("a", "/here", "first")
	s.Messages = append(s.Messages,
		Message{Role: RoleAssistant, Text: ""},
		Message{Role: RoleAssistant, Text: "third"})
	m := store(t, "/here", s)

	got := m.DiskEntries(context.Background(), nil)
	if len(got) != 1 {
		t.Fatalf("read %d conversations", len(got))
	}
	if len(got[0].Texts) != 3 || got[0].Texts[2] != "third" {
		t.Errorf("the messages shifted: %q", got[0].Texts)
	}
}

// The id comes back from a search result rather than from newID, so it is not
// allowed to name a path.
func TestOpenRefusesAnIDThatIsAPath(t *testing.T) {
	m := store(t, "/here", saved("a", "/here", "real"))
	for _, bad := range []string{"", "../../../etc/passwd", `..\..\secrets`, "a/b", "."} {
		if _, _, err := m.Open(bad); err == nil {
			t.Errorf("Open(%q) was allowed", bad)
		}
	}
}

// Opening one that is already open returns the very same session. Two pointers
// for one id would both be saved, each clobbering the other.
func TestOpenOfAnOpenSessionIsTheSameSession(t *testing.T) {
	m := store(t, "/here", saved("a", "/here", "real"))
	m.Restore(5)
	want, n := m.Active(), m.Len()

	got, i, err := m.Open("a")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Error("a second session was made for an id that was already open")
	}
	if m.Len() != n {
		t.Errorf("the list grew to %d", m.Len())
	}
	if m.All()[i] != got {
		t.Errorf("the index %d does not point at the session returned", i)
	}
}

// And one from another project loads, which is the whole point of being able
// to search across them.
func TestOpenLoadsAConversationFromAnotherProject(t *testing.T) {
	m := store(t, "/here", saved("a", "/here", "mine"), saved("b", "/elsewhere", "theirs"))
	m.Restore(5)

	got, i, err := m.Open("b")
	if err != nil {
		t.Fatalf("a conversation from another project would not open: %v", err)
	}
	if got.Root != "/elsewhere" {
		t.Errorf("opened %q", got.Root)
	}
	if m.All()[i] != got {
		t.Error("the index does not point at the session returned")
	}
	if m.Active().ID == "b" {
		t.Error("opening a conversation also selected it; that is the caller's decision")
	}
}
