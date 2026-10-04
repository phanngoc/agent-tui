package memory

import (
	"strings"
	"testing"
)

func TestStorePutReplaceDeleteAndLog(t *testing.T) {
	st := Open(t.TempDir(), Project)
	a, err := st.Put(Record{Content: "The search index skips files over 2 MB", Type: TypeWorkFact, Priority: 80})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := st.Put(Record{Content: "Ranking prefers shorter paths", Type: TypeWorkFact, Priority: 70})
	if a.Version != 1 || a.Scope != Project {
		t.Fatalf("new record: %+v", a)
	}
	m, err := st.Replace([]string{a.ID, b.ID}, Record{Content: "merged", Type: TypeWorkFact, Priority: 85}, "merge")
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != 2 || len(st.All()) != 1 {
		t.Fatalf("merge: version %d, %d records", m.Version, len(st.All()))
	}
	if _, err := st.Put(Record{Content: "x", Type: "nonsense"}); err == nil {
		t.Fatal("an unknown type was accepted")
	}
	if n, _ := st.Delete(m.ID); n != 1 {
		t.Fatalf("deleted %d", n)
	}
	log := st.Log(10)
	if len(log) != 4 || log[0].Op != "delete" || log[1].Op != "merge" || len(log[1].Targets) != 2 {
		t.Fatalf("log: %+v", log)
	}
	// A second handle on the same folder sees the first one's writes.
	if got := Open(st.Dir, Project).All(); len(got) != 0 {
		t.Fatalf("reopened store has %d records", len(got))
	}
}

func TestSearchRanksAndRecallThresholds(t *testing.T) {
	recs := []Record{
		{ID: "1", Content: "Use pnpm, never npm, in the web folder", Type: TypeInstruction},
		{ID: "2", Content: "Search ranking prefers shorter paths", Type: TypeWorkFact},
		{ID: "3", Content: "The user writes commit messages in English", Type: TypePersona},
	}
	hits := Search(recs, "how does ranking of search work", 0)
	if len(hits) == 0 || hits[0].Record.ID != "2" {
		t.Fatalf("hits: %+v", hits)
	}
	if got := Search(recs, "kubernetes", 0); len(got) != 0 {
		t.Fatalf("unrelated query matched: %+v", got)
	}
	// Few matches: all of them, whatever their score.
	if got := Recall(recs, "npm web", 5, 0.99); len(got) != 1 {
		t.Fatalf("small-corpus recall: %+v", got)
	}
}

func TestTokensKeepDiacriticsAndSplitCJK(t *testing.T) {
	got := strings.Join(Tokens("Lưu vào memory_store 検索"), "|")
	if got != "lưu|vào|memory_store|検|索" {
		t.Fatalf("tokens: %s", got)
	}
}

func TestScenesAndPersona(t *testing.T) {
	st := Open(t.TempDir(), Global)
	sc, err := st.PutScene(Scene{File: "Review style!", Summary: "how the user reviews", Heat: 2, Body: "## Key facts\n- terse"})
	if err != nil {
		t.Fatal(err)
	}
	if sc.File != "Review-style.md" {
		t.Fatalf("file name %q", sc.File)
	}
	got := st.Scenes()
	if len(got) != 1 || got[0].Summary != "how the user reviews" || got[0].Heat != 2 || !strings.Contains(got[0].Body, "terse") {
		t.Fatalf("scenes: %+v", got)
	}
	if _, err := st.Scene("../x.md"); err == nil {
		t.Fatal("a path outside the scenes folder was read")
	}
	_ = st.SetPersona("first")
	_ = st.SetPersona("second")
	if st.Persona() != "second" {
		t.Fatal(st.Persona())
	}
}

func TestSystemContextAlwaysIncludesStandingRules(t *testing.T) {
	g := Open(t.TempDir(), Global)
	b := &Bank{Global: g}
	_, _ = g.Put(Record{Content: "Never push to main", Type: TypeInstruction, Priority: -1})
	_, _ = g.Put(Record{Content: "Likes tables", Type: TypePersona, Priority: 60})
	ctx := b.SystemContext()
	if !strings.Contains(ctx, "Never push to main") || strings.Contains(ctx, "Likes tables") {
		t.Fatalf("context:\n%s", ctx)
	}
	if hits := b.Recall("never push"); len(hits) != 0 {
		t.Fatalf("a standing rule was recalled twice: %+v", hits)
	}
	if hits := b.Recall("tables"); len(hits) != 1 {
		t.Fatalf("recall: %+v", hits)
	}
	if r, _ := g.All(), 0; r[0].Hits+r[1].Hits != 1 {
		t.Fatal("recall did not count its hit")
	}
}

func TestStem(t *testing.T) {
	for in, want := range map[string]string{"deploys": "deploy", "deploying": "deploy", "processes": "process",
		"process": "process", "status": "status", "queries": "query", "test": "test", "những": "những"} {
		if got := stem(in); got != want {
			t.Errorf("stem(%q) = %q, want %q", in, got, want)
		}
	}
}
