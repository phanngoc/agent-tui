package wiki

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func TestPageRoundTrip(t *testing.T) {
	p := Page{ID: "entities/redis", Type: TypeEntity, Title: "Redis: cache", Description: "The cache, keyed by user",
		Sources: []string{"design.md", "ops/runbook.md"}, Tags: []string{"infra"}, Locked: true,
		Body: "Links to [[Session store|sessions]] and [[Gateway]]."}
	got := ParsePage(p.ID, p.String())
	if got.Title != p.Title || got.Description != p.Description || got.Type != p.Type || !got.Locked {
		t.Fatalf("header lost: %+v", got)
	}
	if strings.Join(got.Sources, ",") != "design.md,ops/runbook.md" || strings.Join(got.Tags, ",") != "infra" {
		t.Fatalf("lists lost: %+v", got)
	}
	if got.Body != p.Body {
		t.Fatalf("body = %q", got.Body)
	}
	if l := got.Links(); strings.Join(l, ",") != "Session store,Gateway" {
		t.Fatalf("links = %v", l)
	}
}

func TestParsePageInlineLists(t *testing.T) {
	p := ParsePage("x/y", "\xef\xbb\xbf---\r\ntype: concept\r\ntitle: 'It''s'\r\nsources: [a.md, \"b, c.md\"]\r\n---\r\nbody\r\n")
	if p.Title != "It's" || strings.Join(p.Sources, "|") != "a.md|b, c.md" || p.Body != "body" {
		t.Fatalf("%+v", p)
	}
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"Redis Cluster":     "redis-cluster",
		"  API: /v1/users ": "api-v1-users",
		"Đăng nhập (Login)": "đăng-nhập-login",
		"本人確認 eKYC":         "本人確認-ekyc",
		"!!!":               "page",
	} {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
	if got := CanonicalID("methodology", "Code Review"); got != "concepts/code-review" {
		t.Errorf("CanonicalID = %q", got)
	}
}

func TestParseFileBlocks(t *testing.T) {
	text := `Sure!
<<<FILE path="wiki/entities/a.md">>>
---
type: entity
title: A
---
alpha
<<<END>>>
noise
<<<FILE path="../../etc/passwd">>>
evil
<<<END>>>
<<<FILE path="pages/concepts/b.md">>
---
title: B
---
beta
<<<FILE path="pages/concepts/c.md">>>
cut off`
	got := ParseFileBlocks(text)
	if len(got) != 1 {
		t.Fatalf("got %d blocks: %+v", len(got), got)
	}
	if got[0].Path != "pages/entities/a.md" || !strings.Contains(got[0].Body, "alpha") {
		t.Fatalf("%+v", got[0])
	}
}

func TestChunk(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 40; i++ {
		b.WriteString("## Mục " + strings.Repeat("x", 3) + "\n\n" + strings.Repeat("dữ liệu ", 120) + "\n\n")
	}
	text := b.String()
	chunks := Chunk(text, 4000, 200)
	if len(chunks) < 2 {
		t.Fatalf("not split: %d", len(chunks))
	}
	for i, c := range chunks {
		if !utf8.ValidString(c) {
			t.Fatalf("chunk %d cuts a character", i)
		}
		if len(c) > 4000+200 {
			t.Fatalf("chunk %d is %d long", i, len(c))
		}
	}
	if one := Chunk("short", 4000, 200); len(one) != 1 || one[0] != "short" {
		t.Fatalf("%v", one)
	}
}

func seed(t *testing.T, w *Wiki, pages ...Page) {
	t.Helper()
	for _, p := range pages {
		if _, err := w.Put(p, ""); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSearchAndGraph(t *testing.T) {
	w := Open(t.TempDir())
	seed(t, w,
		Page{Type: TypeEntity, Title: "Login screen", Description: "Where users sign in", Sources: []string{"s.md"},
			Body: "Calls [[Auth API]]. Users enter email and password."},
		Page{Type: TypeEntity, Title: "Auth API", Sources: []string{"s.md"}, Body: "Issues tokens. Stored by [[Token store]]."},
		Page{Type: TypeEntity, Title: "Token store", Sources: []string{"s.md"}, Body: "Redis keys per session."},
		Page{Type: TypeConcept, Title: "Billing", Sources: []string{"b.md"}, Body: "Invoices monthly. See [[Nowhere]]."},
	)
	res := w.Search(Query{Text: "login password"})
	if len(res) != 1 || res[0].ID != "entities/login-screen" || res[0].Score != 1 {
		t.Fatalf("plain search: %+v", res)
	}
	if len(res[0].Related) != 1 || res[0].Related[0].ID != "entities/auth-api" || res[0].Related[0].Dir != "out" {
		t.Fatalf("related: %+v", res[0].Related)
	}
	res = w.Search(Query{Text: "login password", Hops: 2})
	ids := []string{}
	for _, r := range res {
		ids = append(ids, r.ID)
	}
	if strings.Join(ids, ",") != "entities/login-screen,entities/auth-api,entities/token-store" {
		t.Fatalf("hops: %v", ids)
	}
	if res[1].Hop != 1 || res[1].Via != "Login screen" || res[2].Score != 0.25 {
		t.Fatalf("hop scores: %+v", res)
	}
	// Title outweighs body.
	res = w.Search(Query{Text: "token"})
	if res[0].ID != "entities/token-store" {
		t.Fatalf("title weight: %+v", res)
	}
	l := w.Lint()
	if len(l.Broken["concepts/billing"]) != 1 || strings.Join(l.Orphans, ",") != "concepts/billing,entities/login-screen" {
		t.Fatalf("lint: %+v", l)
	}
	g := NewGraph(w.Pages())
	for _, ref := range []string{"Auth API", "auth-api", "entities/auth-api", "pages/entities/auth-api.md"} {
		if id, ok := g.Resolve(ref); !ok || id != "entities/auth-api" {
			t.Errorf("Resolve(%q) = %q %v", ref, id, ok)
		}
	}
}

func TestPutKeepsHistoryAndIndex(t *testing.T) {
	w := Open(t.TempDir())
	p, err := w.Put(Page{Type: TypeConcept, Title: "Retry policy", Body: "v1"}, "**edit** by hand")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Put(Page{Type: TypeConcept, Title: "Retry policy", Body: "v2"}, "**edit** by hand"); err != nil {
		t.Fatal(err)
	}
	if h := w.History(p.ID); len(h) != 1 {
		t.Fatalf("history: %v", h)
	}
	if got, _ := w.Get(p.ID); got.Body != "v2" {
		t.Fatalf("body %q", got.Body)
	}
	if !strings.Contains(w.Index(), "[[Retry policy]]") || strings.Count(w.Log(), "**edit**") != 2 {
		t.Fatalf("index/log:\n%s\n%s", w.Index(), w.Log())
	}
	if _, err := w.Put(Page{Type: TypeConcept, ID: "../x", Title: "x", Body: "x"}, ""); err == nil {
		t.Fatal("an id that climbs out was written")
	}
	if _, err := w.AddRaw("../../evil.md", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(w.RawDir(), "evil.md")); err != nil {
		t.Fatal("a climbing raw name was not kept inside raw/")
	}
	if _, err := w.AddRaw("x.pdf", []byte("%PDF\x00\x01")); err == nil {
		t.Fatal("binary accepted")
	}
}

func TestAddNote(t *testing.T) {
	w := Open(t.TempDir())
	name, err := w.AddNote(Note{Content: "## Retry\nPayments retry 3 times.", From: "the conversation «bug» (abc), message 4"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(name, "notes/") || !strings.HasSuffix(name, "-retry.md") {
		t.Fatalf("name %q: the title comes from the first line", name)
	}
	text, _ := w.ReadRaw(name)
	if !strings.HasPrefix(text, "# Retry\n") || !strings.Contains(text, "message 4") || !strings.Contains(text, "Payments retry 3 times.") {
		t.Fatalf("document:\n%s", text)
	}
	if _, err := w.AddNote(Note{Title: "x", Content: "  "}); err == nil {
		t.Fatal("an empty note was kept")
	}
}

// fake answers each stage by its system prompt.
type fake struct {
	mu    sync.Mutex
	calls map[string]int
	pages map[string]string // source name -> FILE blocks
}

func (f *fake) Complete(_ context.Context, system, user string, _ int64) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	switch {
	case strings.HasPrefix(system, "You are a knowledge base analyst"):
		f.calls["analysis"]++
		return "Entities: Auth API (exists)", nil
	case strings.HasPrefix(system, "You are a meticulous"):
		f.calls["generate"]++
		for name, blocks := range f.pages {
			if strings.Contains(user, "## Source document: "+name) {
				return blocks, nil
			}
		}
		return "no pages, sorry", nil
	case strings.HasPrefix(system, "Merge two"):
		f.calls["merge"]++
		return "---\ntype: entity\ntitle: Auth API\ndescription: merged\n---\nIssues tokens. Rate limited.", nil
	case strings.HasPrefix(system, "An existing wiki page is long"):
		f.calls["append"]++
		return "## More\nnew fact", nil
	case strings.HasPrefix(system, "You write the overview"):
		f.calls["overview"]++
		return "This wiki covers [[Auth API]].", nil
	}
	return "", nil
}

func block(path, typ, title, body string) string {
	return "<<<FILE path=\"" + path + "\">>>\n---\ntype: " + typ + "\ntitle: " + title + "\n---\n" + body + "\n<<<END>>>\n"
}

func TestIngest(t *testing.T) {
	w := Open(t.TempDir())
	f := &fake{pages: map[string]string{
		"auth.md": block("pages/sources/auth.md", "source", "Auth design", "Describes [[Auth API]].") +
			block("pages/entities/whatever.md", "entity", "Auth API", "Issues tokens."),
		"limits.md": block("pages/sources/limits.md", "source", "Limits", "Rate limits for [[Auth API]].") +
			block("pages/entities/auth.md", "entity", "Auth API", "Rate limited to 10 rps."),
		"broken.md": "I could not do it",
	}}
	for name, body := range map[string]string{"auth.md": "# Auth\ntokens", "limits.md": "# Limits\n10 rps", "broken.md": "x"} {
		if _, err := w.AddRaw(name, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := w.Ingest(context.Background(), f, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Ingested) != 2 || len(rep.Failed) != 1 || rep.Failed["broken.md"] == "" {
		t.Fatalf("report: %+v", rep)
	}
	api, ok := w.Get("entities/auth-api")
	if !ok || f.calls["merge"] != 1 || api.Description != "merged" {
		t.Fatalf("merge: %+v calls %v", api, f.calls)
	}
	if strings.Join(api.Sources, ",") != "auth.md,limits.md" {
		t.Fatalf("sources: %v", api.Sources)
	}
	if !strings.Contains(w.Overview(), "[[Auth API]]") || !strings.Contains(w.Index(), "[[Limits]]") {
		t.Fatalf("overview/index missing")
	}
	if _, err := os.Stat(filepath.Join(w.Dir, "_debug")); err != nil {
		t.Fatal("unreadable answer not kept")
	}

	// Nothing changed: nothing is read again, the failed one is retried.
	f.calls = nil
	rep, _ = w.Ingest(context.Background(), f, nil)
	if rep.Skipped != 2 || f.calls["generate"] != 1 {
		t.Fatalf("second run: %+v %v", rep, f.calls)
	}

	// A removed document takes its own pages with it and leaves shared ones.
	if err := w.RemoveRaw("limits.md"); err != nil {
		t.Fatal(err)
	}
	rep, _ = w.Ingest(context.Background(), f, nil)
	if strings.Join(rep.Deleted, ",") != "limits.md" {
		t.Fatalf("deleted: %+v", rep)
	}
	if _, ok := w.Get("sources/limits"); ok {
		t.Fatal("a page only the removed document wrote survived")
	}
	api, _ = w.Get("entities/auth-api")
	if strings.Join(api.Sources, ",") != "auth.md" {
		t.Fatalf("shared page sources: %v", api.Sources)
	}

	// A locked page is not merged into.
	api.Locked = true
	api.Body = "hand written"
	if _, err := w.Put(api, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := w.AddRaw("auth.md", []byte("# Auth\ntokens, changed")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Ingest(context.Background(), f, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.Get("entities/auth-api"); got.Body != "hand written" {
		t.Fatalf("locked page changed: %q", got.Body)
	}
}

func TestIngestBusy(t *testing.T) {
	w := Open(t.TempDir())
	_ = w.Init()
	unlock, err := w.lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := w.Ingest(context.Background(), &fake{}, nil); err != ErrBusy {
		t.Fatalf("err = %v", err)
	}
}
