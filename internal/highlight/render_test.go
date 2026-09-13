package highlight

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func testScheme() *Scheme {
	c := lipgloss.Color
	return NewScheme(c("#dbe2ee"), c("#d3a3f0"), c("#8fe3d3"), c("#cbeda0"),
		c("#fb9d80"), c("#8593a6"), c("#93b6ff"), c("#aabbd4"))
}

func plain(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = stripEsc(l)
	}
	return out
}

// stripEsc removes SGR sequences; the renderer only ever emits those.
func stripEsc(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			i++
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func TestRenderPreservesLineCount(t *testing.T) {
	src := "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n"
	got := Render(testScheme(), Detect("main.go"), []byte(src))
	if want := 5; len(got) != want {
		t.Fatalf("line count = %d, want %d: %q", len(got), want, plain(got))
	}
	if p := plain(got); p[0] != "package main" || p[3] != "    println(\"hi\")" {
		t.Errorf("plain text changed: %q", p)
	}
}

func TestRenderMultiLineConstructs(t *testing.T) {
	cases := []struct {
		name, file, src string
		wantLines       int
	}{
		{"block comment", "a.go", "a\n/* one\ntwo */\nb\n", 4},
		{"raw string", "a.go", "x := `line1\nline2`\ny := 1\n", 3},
		{"python docstring", "a.py", "def f():\n    \"\"\"doc\n    more\n    \"\"\"\n    return 1\n", 5},
		{"unterminated string", "a.go", "s := \"oops\nnext\n", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Render(testScheme(), Detect(tc.file), []byte(tc.src))
			if len(got) != tc.wantLines {
				t.Fatalf("lines = %d, want %d: %q", len(got), tc.wantLines, plain(got))
			}
			// Every line must be self-contained: no escape may leak past a
			// newline, or the viewport's per-line slicing would corrupt colour.
			for i, l := range got {
				if strings.Count(l, "\x1b[") > 0 && !strings.HasSuffix(stripTrailing(l), reset) && strings.Contains(l, "\x1b[38") {
					if !strings.Contains(l, reset) {
						t.Errorf("line %d opens a colour it never closes: %q", i, l)
					}
				}
			}
		})
	}
}

func stripTrailing(s string) string { return strings.TrimRight(s, " ") }

func TestRenderRoundTripsText(t *testing.T) {
	// Whatever the lexer does, the visible characters must survive intact
	// (modulo tab expansion), because search maps offsets onto this text.
	src := "const x = 42 // note\nif (a && b) { return \"s\"; }\n"
	got := plain(Render(testScheme(), Detect("a.ts"), []byte(src)))
	if strings.Join(got, "\n")+"\n" != src {
		t.Errorf("text changed:\n got %q\nwant %q", strings.Join(got, "\n")+"\n", src)
	}
}

func TestTabsExpandToColumns(t *testing.T) {
	got := plain(Render(testScheme(), Detect("a.go"), []byte("ab\tc\n")))
	if got[0] != "ab  c" {
		t.Errorf("tab expansion = %q, want %q", got[0], "ab  c")
	}
}

func TestDetect(t *testing.T) {
	for file, want := range map[string]string{
		"main.go": "Go", "app.tsx": "JavaScript/TypeScript", "Makefile": "Makefile",
		"Dockerfile.dev": "Dockerfile", "notes.md": "Markdown", "mystery": "Text",
		"deploy/main.tf": "HCL/Terraform",
	} {
		if got := Detect(file).Name; got != want {
			t.Errorf("Detect(%q) = %q, want %q", file, got, want)
		}
	}
}

var benchSrc = strings.Repeat(
	"func handler(w http.ResponseWriter, r *http.Request) error {\n"+
		"\t// look up the record\n"+
		"\tid := r.URL.Query().Get(\"id\")\n"+
		"\tif id == \"\" {\n\t\treturn fmt.Errorf(\"missing id %d\", 42)\n\t}\n"+
		"\treturn nil\n}\n", 2000)

func BenchmarkRenderGo(b *testing.B) {
	sc, lang, src := testScheme(), Detect("x.go"), []byte(benchSrc)
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		Render(sc, lang, src)
	}
}
