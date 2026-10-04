package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFrontmatter(t *testing.T) {
	meta, body := Parse("\ufeff---\r\nname: deploy\r\ndescription: >\r\n  Install it\r\n  globally\r\nother: 'x'\r\n---\r\n\r\n# Body\r\n")
	if meta["name"] != "deploy" || meta["description"] != "Install it globally" || meta["other"] != "x" {
		t.Fatalf("meta: %#v", meta)
	}
	if body != "# Body\n" {
		t.Fatalf("body: %q", body)
	}
}

func TestStoreScopesShadowAndDisable(t *testing.T) {
	st := Store{GlobalDir: t.TempDir(), ProjectDir: t.TempDir()}
	if _, err := st.Save(Skill{Name: "Bad Name", Description: "x", Scope: Global}); err == nil {
		t.Fatal("an invalid name was saved")
	}
	if _, err := st.Save(Skill{Name: "review", Scope: Global}); err == nil {
		t.Fatal("a skill without a description was saved")
	}
	for _, sk := range []Skill{
		{Name: "review", Description: "global review", Body: "g", Scope: Global},
		{Name: "review", Description: "project review", Body: "p", Scope: Project},
		{Name: "ship", Description: "ship it", Body: "s", Scope: Global, Learned: true},
	} {
		if _, err := st.Save(sk); err != nil {
			t.Fatal(err)
		}
	}
	got, ok := st.Get("review")
	if !ok || got.Scope != Project || got.Body != "p\n" {
		t.Fatalf("get: %+v", got)
	}
	shadowed := 0
	for _, sk := range st.List() {
		if sk.Shadowed {
			shadowed++
		}
	}
	if shadowed != 1 {
		t.Fatalf("%d shadowed", shadowed)
	}
	act := st.Active([]string{"ship"})
	if len(act) != 1 || act[0].Name != "review" {
		t.Fatalf("active: %+v", act)
	}
	if sk, _ := st.Get("ship"); !sk.Learned {
		t.Fatal("learned flag was lost")
	}
	if err := st.Delete(Project, "review"); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Get("review"); got.Scope != Global {
		t.Fatalf("after delete: %+v", got)
	}
}

func TestCopyBringsTheWholeFolder(t *testing.T) {
	src := filepath.Join(t.TempDir(), "docling")
	_ = os.MkdirAll(filepath.Join(src, "scripts"), 0o755)
	_ = os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: docling\ndescription: convert docs\n---\nbody"), 0o644)
	_ = os.WriteFile(filepath.Join(src, "scripts", "run.sh"), []byte("echo"), 0o644)
	st := Store{GlobalDir: t.TempDir()}
	sk, err := st.Copy(src, Global, "docling")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(sk.Files, ",") != "scripts/run.sh" || sk.Description != "convert docs" {
		t.Fatalf("copied: %+v", sk)
	}
}
