package kit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/phanngoc/agent-tui/internal/wiki"
)

func TestWikiInExtras(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()

	k := For(root)
	k.DryRun = true
	x, tr := k.Extras(context.Background(), "api", "how does login work?")
	if strings.Contains(x.System, "<wiki") || tr.Wiki != 0 {
		t.Fatal("an empty wiki was offered")
	}

	w := wiki.For(root)
	for _, p := range []wiki.Page{
		{Type: wiki.TypeEntity, Title: "Login screen", Description: "Where users sign in", Sources: []string{"spec.md"}, Body: "Calls [[Auth API]] with email and password."},
		{Type: wiki.TypeEntity, Title: "Auth API", Sources: []string{"spec.md"}, Body: "Issues tokens."},
	} {
		if _, err := w.Put(p, ""); err != nil {
			t.Fatal(err)
		}
	}

	k = For(root)
	k.DryRun = true
	x, tr = k.Extras(context.Background(), "api", "how does login work?")
	if !strings.Contains(x.System, `<wiki pages="2">`) || !strings.Contains(x.System, "wiki_search") || tr.Wiki != 2 {
		t.Fatalf("system:\n%s", x.System)
	}
	tools := map[string]func(context.Context, json.RawMessage) (string, bool){}
	for _, e := range x.Tools {
		tools[e.Name] = e.Run
	}
	out, _ := tools["wiki_search"](context.Background(), json.RawMessage(`{"query":"login password"}`))
	if !strings.Contains(out, "Login screen") || !strings.Contains(out, "linked: Auth API") {
		t.Fatalf("wiki_search: %q", out)
	}
	out, isErr := tools["wiki_read"](context.Background(), json.RawMessage(`{"refs":["Auth API","index","nope"]}`))
	if isErr || !strings.Contains(out, "Issues tokens.") || !strings.Contains(out, "Linked pages: Login screen") ||
		!strings.Contains(out, "# Index") || !strings.Contains(out, `no page "nope"`) {
		t.Fatalf("wiki_read: %q", out)
	}
	out, isErr = tools["wiki_write"](context.Background(), json.RawMessage(`{"type":"synthesis","title":"Login flow","body":"[[Login screen]] then [[Auth API]]."}`))
	if isErr || !strings.Contains(out, "synthesis/login-flow") {
		t.Fatalf("wiki_write: %q", out)
	}
	if p, ok := w.Get("synthesis/login-flow"); !ok || p.Sources[0] != "agent" {
		t.Fatalf("filed page: %+v", p)
	}

	// A CLI engine is told the tools by their MCP names.
	x, _ = k.Extras(context.Background(), "claude", "login")
	if !strings.Contains(x.System, "mcp__agent-tui__wiki_search") {
		t.Fatalf("CLI system:\n%s", x.System)
	}
}
