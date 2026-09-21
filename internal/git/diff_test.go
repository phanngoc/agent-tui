package git

import (
	"strings"
	"testing"
)

const samplePatch = `diff --git a/deploy/README.md b/deploy/README.md
index 1111111..2222222 100644
--- a/deploy/README.md
+++ b/deploy/README.md
@@ -36,8 +36,8 @@ func serve() {
 kubectl --context fpaas-2161 -n build \
   port-forward service/external-mock 8300:8080

-**Outside the cluster**: ` + "`https://pms-agent.sun-asterisk.vn/external-mock/`" + `
+**Outside the cluster**: ` + "`https://fpaas-mock.sun-asterisk.vn/`" + `

 ` + "```bash" + `
 curl https://example.test/api/health
`

func TestParsePatch(t *testing.T) {
	files := ParsePatch(samplePatch)
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}
	f := files[0]
	if f.Path != "deploy/README.md" {
		t.Errorf("path = %q", f.Path)
	}
	if len(f.Hunks) != 1 {
		t.Fatalf("got %d hunks, want 1", len(f.Hunks))
	}
	h := f.Hunks[0]
	if h.Header != "func serve() {" {
		t.Errorf("hunk header = %q", h.Header)
	}
	if f.Added() != 1 || f.Deleted() != 1 {
		t.Errorf("+%d -%d, want +1 -1", f.Added(), f.Deleted())
	}
}

// TestLineNumbersFollowBothSides is what makes a diff readable: every line has
// to say where it is in the file, and the two sides advance independently.
func TestLineNumbersFollowBothSides(t *testing.T) {
	f := ParsePatch(samplePatch)[0]
	lines := f.Hunks[0].Lines

	var lastOld, lastNew int
	for _, l := range lines {
		switch l.Kind {
		case Context:
			if l.Old <= lastOld || l.New <= lastNew {
				t.Errorf("context line %q did not advance both sides (%d,%d)", l.Text, l.Old, l.New)
			}
			lastOld, lastNew = l.Old, l.New
		case Deleted:
			if l.New != 0 {
				t.Errorf("a deleted line claims a new-side number: %q", l.Text)
			}
			if l.Old <= lastOld {
				t.Errorf("deleted line did not advance the old side: %q", l.Text)
			}
			lastOld = l.Old
		case Added:
			if l.Old != 0 {
				t.Errorf("an added line claims an old-side number: %q", l.Text)
			}
			if l.New <= lastNew {
				t.Errorf("added line did not advance the new side: %q", l.Text)
			}
			lastNew = l.New
		}
	}
	// The header said the hunk starts at 36 on both sides.
	if lines[0].Old != 36 || lines[0].New != 36 {
		t.Errorf("first line is (%d,%d), want (36,36)", lines[0].Old, lines[0].New)
	}
}

// TestSpansFindTheWordThatChanged is the whole point of the word-level pass.
func TestSpansFindTheWordThatChanged(t *testing.T) {
	f := ParsePatch(samplePatch)[0]
	var del, add Line
	for _, l := range f.Hunks[0].Lines {
		switch l.Kind {
		case Deleted:
			del = l
		case Added:
			add = l
		}
	}
	if len(del.Spans) != 1 || len(add.Spans) != 1 {
		t.Fatalf("spans: deleted=%v added=%v", del.Spans, add.Spans)
	}
	gotDel := del.Text[del.Spans[0].Start:del.Spans[0].End]
	gotAdd := add.Text[add.Spans[0].Start:add.Spans[0].End]

	// The shared "https://" before and ".sun-asterisk.vn" after must stay out
	// of it; only the host and the path that follows it changed.
	if !strings.Contains(gotDel, "pms-agent") || strings.Contains(gotDel, "https://") {
		t.Errorf("deleted span = %q", gotDel)
	}
	if !strings.Contains(gotAdd, "fpaas-mock") || strings.Contains(gotAdd, "https://") {
		t.Errorf("added span = %q", gotAdd)
	}
}

func TestDiffSpans(t *testing.T) {
	cases := []struct {
		name, a, b string
		wantA      string
		wantB      string
	}{
		{"one word", "call foo(x)", "call bar(x)", "foo", "bar"},
		{"suffix grows", "let a = 1", "let a = 12", "", "2"},
		{"prefix grows", "value", "myvalue", "", "my"},
		// A whole-line rewrite gets no spans: painting almost everything says
		// nothing the flat colour did not already say.
		{"rewritten", "alpha beta gamma", "one two three four", "", ""},
		{"identical", "same", "same", "", ""},
		{"empty side", "", "added", "", ""},
	}
	for _, c := range cases {
		sa, sb := diffSpans(c.a, c.b)
		gotA, gotB := "", ""
		if len(sa) == 1 {
			gotA = c.a[sa[0].Start:sa[0].End]
		}
		if len(sb) == 1 {
			gotB = c.b[sb[0].Start:sb[0].End]
		}
		if gotA != c.wantA || gotB != c.wantB {
			t.Errorf("%s: diffSpans(%q,%q) = %q,%q want %q,%q",
				c.name, c.a, c.b, gotA, gotB, c.wantA, c.wantB)
		}
	}
}

// TestSpansNeverSplitARune guards the one way this can corrupt the display:
// slicing a multi-byte character in half.
func TestSpansNeverSplitARune(t *testing.T) {
	pairs := [][2]string{
		{"giá trị cũ ở đây", "giá trị mới ở đây"},
		{"日本語のテキスト", "日本語のコード"},
		{"emoji ✓ here", "emoji ✗ here"},
	}
	for _, p := range pairs {
		sa, sb := diffSpans(p[0], p[1])
		for _, s := range sa {
			if !utf8Whole(p[0], s) {
				t.Errorf("span %v splits a rune in %q", s, p[0])
			}
		}
		for _, s := range sb {
			if !utf8Whole(p[1], s) {
				t.Errorf("span %v splits a rune in %q", s, p[1])
			}
		}
	}
}

func utf8Whole(s string, sp Span) bool {
	if sp.Start < 0 || sp.End > len(s) || sp.Start > sp.End {
		return false
	}
	return !isCont(s[sp.Start]) && (sp.End == len(s) || !isCont(s[sp.End]))
}

func TestParsePatchHandlesRenamesAndBinaries(t *testing.T) {
	patch := `diff --git a/old/name.go b/new/name.go
similarity index 98%
rename from old/name.go
rename to new/name.go
diff --git a/logo.png b/logo.png
index 3333333..4444444 100644
Binary files a/logo.png and b/logo.png differ
`
	files := ParsePatch(patch)
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2", len(files))
	}
	if files[0].Old != "old/name.go" || files[0].Path != "new/name.go" {
		t.Errorf("rename = %q -> %q", files[0].Old, files[0].Path)
	}
	if !files[1].Binary {
		t.Error("the image was not marked binary")
	}
}

func TestParsePatchHandlesSeveralHunks(t *testing.T) {
	patch := `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,3 +1,3 @@
 one
-two
+TWO
@@ -10,3 +10,4 @@ func f() {
 ten
+eleven
 twelve
`
	f := ParsePatch(patch)[0]
	if len(f.Hunks) != 2 {
		t.Fatalf("got %d hunks, want 2", len(f.Hunks))
	}
	if f.Hunks[1].Lines[0].Old != 10 {
		t.Errorf("second hunk starts at old line %d, want 10", f.Hunks[1].Lines[0].Old)
	}
	if f.Added() != 2 || f.Deleted() != 1 {
		t.Errorf("+%d -%d, want +2 -1", f.Added(), f.Deleted())
	}
}

func TestParseHunkHeader(t *testing.T) {
	cases := []struct {
		in       string
		old, new int
		ok       bool
	}{
		{"@@ -36,8 +36,8 @@ func serve() {", 36, 36, true},
		{"@@ -1 +1 @@", 1, 1, true},
		{"@@ -0,0 +1,5 @@", 0, 1, true},
		{"not a hunk", 0, 0, false},
		{"@@ broken", 0, 0, false},
	}
	for _, c := range cases {
		o, n, ok := parseHunkHeader(c.in)
		if ok != c.ok || (ok && (o != c.old || n != c.new)) {
			t.Errorf("parseHunkHeader(%q) = %d,%d,%v want %d,%d,%v",
				c.in, o, n, ok, c.old, c.new, c.ok)
		}
	}
}

func TestParseLog(t *testing.T) {
	rec := func(f ...string) string { return strings.Join(f, fieldSep) + recordSep }
	in := rec("abc123def", "abc123d", "ngocp", "2026-09-16T11:24:00+07:00",
		"HEAD -> feat/x, origin/feat/x", "p1 p2", "feat: subject line", "body\ntext") +
		"\n" + rec("999", "999", "someone", "2026-09-15T09:00:00+07:00", "", "p0", "plain", "")

	got := parseLog(in)
	if len(got) != 2 {
		t.Fatalf("got %d commits, want 2", len(got))
	}
	c := got[0]
	if c.SHA != "abc123def" || c.Short != "abc123d" || c.Author != "ngocp" {
		t.Errorf("commit = %+v", c)
	}
	if c.Subject != "feat: subject line" {
		t.Errorf("subject = %q", c.Subject)
	}
	if c.Body != "body\ntext" {
		t.Errorf("body = %q", c.Body)
	}
	if !c.Merge() {
		t.Error("two parents should read as a merge")
	}
	if len(c.Refs) != 2 || c.Refs[0] != "HEAD -> feat/x" {
		t.Errorf("refs = %v", c.Refs)
	}
	if c.When.Year() != 2026 || c.When.Day() != 16 {
		t.Errorf("when = %v", c.When)
	}
	if got[1].Merge() {
		t.Error("one parent is not a merge")
	}
	if len(got[1].Refs) != 0 {
		t.Errorf("refs = %v, want none", got[1].Refs)
	}
}

// TestParseLogSurvivesAwkwardSubjects is why the format is delimited by
// control bytes: a subject may contain anything a shell or a tab would.
func TestParseLogSurvivesAwkwardSubjects(t *testing.T) {
	subject := "fix: handle a\ttab, a | pipe and a \"quote\""
	in := strings.Join([]string{
		"sha", "sh", "me", "2026-01-01T00:00:00Z", "", "p", subject, "",
	}, fieldSep) + recordSep

	got := parseLog(in)
	if len(got) != 1 {
		t.Fatalf("got %d commits", len(got))
	}
	if got[0].Subject != subject {
		t.Errorf("subject = %q, want %q", got[0].Subject, subject)
	}
}

func TestParseNumstat(t *testing.T) {
	in := "12\t3\tinternal/ui/view.go\x005\t0\tREADME.md\x00-\t-\tlogo.png\x00"
	got := parseNumstat(in)
	if len(got) != 3 {
		t.Fatalf("got %d files, want 3: %+v", len(got), got)
	}
	if got[0].Path != "internal/ui/view.go" || got[0].Added != 12 || got[0].Deleted != 3 {
		t.Errorf("first = %+v", got[0])
	}
	if !got[2].Binary {
		t.Errorf("logo.png should be binary: %+v", got[2])
	}
}

func TestParseNumstatRename(t *testing.T) {
	// -z spends three fields on a rename: counts with an empty path, then the
	// old name, then the new one.
	in := "4\t2\t\x00old/name.go\x00new/name.go\x001\t1\tother.go\x00"
	got := parseNumstat(in)
	if len(got) != 2 {
		t.Fatalf("got %d files, want 2: %+v", len(got), got)
	}
	if got[0].Old != "old/name.go" || got[0].Path != "new/name.go" {
		t.Errorf("rename = %+v", got[0])
	}
	if got[1].Path != "other.go" {
		t.Errorf("the file after a rename was lost: %+v", got[1])
	}
}
