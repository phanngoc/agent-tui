package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/phanngoc/agent-tui/internal/git"
	"github.com/phanngoc/agent-tui/internal/session"
)

// A column is measured in cells, so whatever is put in one has to be cut in
// cells. Deciding by width and then cutting by runes agrees only for text that
// is entirely half-width — and a repository whose commits and documents are in
// Japanese is not that.
func TestTruncateNeverExceedsItsWidth(t *testing.T) {
	for _, s := range []string{
		"a plain ascii line that is quite long and will need cutting",
		"認証はリクエストパラメータの APID で行う、ヘッダは使用しない",
		"docs/fxt-connectivity.md の 電文確認書 — mixed 日本語 and ascii",
		"「提携アプリ：アプリ名_バージョン情報」の書式に従い",
		"\x1b[31mstyled\x1b[0m 日本語 text with escapes in it",
		"émoji ☑ and combining marks áéíóú",
	} {
		for _, w := range []int{2, 5, 10, 20, 40, 95} {
			got := truncate(s, w)
			if n := lipgloss.Width(got); n > w {
				t.Errorf("truncate(%q, %d) is %d columns wide:\n  %q", s, w, n, got)
			}
		}
	}
	// Text that already fits comes back untouched, ellipsis and all.
	if got := truncate("短い", 10); got != "短い" {
		t.Errorf("a line that fits was changed to %q", got)
	}
}

// The regression this file exists for: a diff of Japanese text ran past its
// column, wrapped, and the overflow landed at column zero — inside the commit
// list beside it.
func TestGitPanesStayInTheirColumns(t *testing.T) {
	m := newTestModel(t)
	subject := "fix(onboarding): drop 名寄せ-excluded services from " +
		"顧客情報管理システムに管理されている設定値と照合する方式で"
	m.git = fillGit(&gitState{
		root:  "/repo",
		shown: "a",
		commits: []git.Commit{
			{SHA: "a", Short: "a1b2c3d", Subject: subject, Author: "loanld-0752"},
			{SHA: "b", Short: "b2c3d4e", Subject: "feat(shinsei): send exactly the 電文一覧", Author: "phan.ngoc"},
		},
		diff: []git.File{{
			Path: "docs/fxt-connectivity.md",
			Hunks: []git.Hunk{{
				Header: "Two things have to land before it can, and they are in that order:",
				Lines: []git.Line{
					{Kind: git.Added, New: 552, Text: "> 認証はリクエストパラメータの `APID`"},
					{Kind: git.Added, New: 553, Text: "> HTTP Header の `Authorization`、Bearerトークン、APIキー等は **使用しない**"},
					{Kind: git.Added, New: 565, Text: "> `{AppName}` は「提携アプリ：アプリ名_バージョン情報」の書式に従い"},
					{Kind: git.Deleted, Old: 566, Text: "The code sent `SuperAppli_0.0.0`, from an earlier reading of スーパーアプリ"},
				},
			}},
		}},
		files: []git.FileChange{{Path: "docs/fxt-connectivity.md", Added: 30, Deleted: 2}},
	})
	m.overlay = overlayGit

	listW := m.gitListWidth()
	diffW := m.gitWidth() - 2 - listW - 3

	for i, l := range m.gitListRows(listW, m.gitRows()) {
		if n := lipgloss.Width(l); n > listW {
			t.Errorf("commit row %d is %d columns in a %d column list:\n  %q",
				i, n, listW, stripANSI(l))
		}
	}
	for i, l := range m.gitDiffRows(diffW, m.gitRows()) {
		if n := lipgloss.Width(l); n > diffW {
			t.Errorf("diff row %d is %d columns in a %d column pane:\n  %q",
				i, n, diffW, stripANSI(l))
		}
	}

	// And the whole browser is a rectangle: one row wider than the rest is a
	// row that wraps, and what it wraps into is the column beside it.
	w := m.gitWidth()
	for i, l := range strings.Split(m.gitView(), "\n") {
		if n := lipgloss.Width(l); n != w {
			t.Errorf("browser row %d is %d columns, want %d:\n  %q", i, n, w, stripANSI(l))
		}
	}
}

// The transcript is one column too, and an answer full of Japanese is the
// common case in this repository rather than an exotic one.
func TestTranscriptStaysInItsColumn(t *testing.T) {
	const w = 60
	src := strings.Join([]string{
		"| Host | 役割 |",
		"|---|---|",
		"| `mail.fpaas-dev.sun-asterisk.vn` | SES ドメイン識別子、DKIM 三件 |",
		"",
		"認証はリクエストパラメータの `APID` で行い、HTTP ヘッダの `Authorization` は使用しない。",
		"",
		"- `SE10021` タイムアウト操作 UI **10分** → リトライ可能",
	}, "\n")

	st, sc := mdFixture()
	for i, l := range renderMarkdown(st, sc, src, w) {
		if n := lipgloss.Width(l); n > w {
			t.Errorf("line %d is %d columns wide, want at most %d:\n  %q", i, n, w, stripANSI(l))
		}
	}
}

// A tab is one character and four columns. Everything that lays text out in
// columns has to agree with the terminal about which of those a line is
// measured in — a diff of Go source is tabs all the way down the left, and a
// row measured at nothing per tab and drawn at four runs into the pane beside
// it.
func TestTabsAreCountedAsTheyAreDrawn(t *testing.T) {
	m := newTestModel(t)
	// The patch is what expands them, so the lines arrive here already widened.
	patch := "diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -1,2 +1,3 @@\n" +
		"+\t\t\tif err := doTheThing(ctx, a, b); err != nil {\n" +
		"+\t\t\t\treturn fmt.Errorf(\"a reasonably long wrapped message: %w\", err)\n" +
		" \tcontext line\n"
	files := git.ParsePatch(patch)
	if len(files) == 0 || len(files[0].Hunks) == 0 {
		t.Fatal("the patch did not parse")
	}
	for _, l := range files[0].Hunks[0].Lines {
		if strings.ContainsRune(l.Text, '\t') {
			t.Errorf("a tab survived into a diff line: %q", l.Text)
		}
	}

	m.git = fillGit(&gitState{
		root: "/repo", shown: "a",
		commits: []git.Commit{{SHA: "a", Subject: "tabs"}},
		diff:    files,
		files:   []git.FileChange{{Path: "x.go", Added: 2}},
	})
	m.overlay = overlayGit

	w := m.gitWidth() - 2 - m.gitListWidth() - 3
	for i, l := range m.gitDiffRows(w, m.gitRows()) {
		if n := lipgloss.Width(l); n > w {
			t.Errorf("diff row %d is %d columns in a %d column pane:\n  %q",
				i, n, w, stripANSI(l))
		}
	}
	// The transcript shows command output, and a test summary is a table made
	// of tabs.
	var b strings.Builder
	m.renderShell(&b, &session.ShellRun{
		Where: "host", Command: "go test ./...", Done: true,
		Output: "ok\tgithub.com/phanngoc/agent-tui/internal/ui\t43.298s\n" +
			"--- FAIL: TestFoo\n\tfoo_test.go:42: \twanted x, got y\n",
	}, "", 60)
	for i, l := range strings.Split(strings.TrimRight(b.String(), "\n"), "\n") {
		if n := lipgloss.Width(l); n > 60 {
			t.Errorf("shell row %d is %d columns, want at most 60:\n  %q", i, n, stripANSI(l))
		}
	}
}
