package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write puts a theme file where Resolve will look for it.
func write(t *testing.T, name, body string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "cfg")
	t.Setenv("XDG_CONFIG_HOME", dir)
	themes := filepath.Join(dir, "agent-tui", "themes")
	if err := os.MkdirAll(themes, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(themes, name+".json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A theme file says what it wants changed; everything else stays measured.
// That is what makes a four-line theme worth writing.
func TestAThemeOnlySaysWhatItChanges(t *testing.T) {
	write(t, "mine", `{"accent": "#7aa2f7", "success": "#9ece6a"}`)

	p, err := Resolve("mine")
	if err != nil {
		t.Fatalf("a two-colour theme was refused: %v", err)
	}
	if got := hex(p.Accent); got != "#7aa2f7" {
		t.Errorf("accent is %s", got)
	}
	if got := hex(p.Good); got != "#9ece6a" {
		t.Errorf("success is %s", got)
	}
	// And everything it did not mention is the default's, unchanged.
	if hex(p.Bg) != hex(OneDark.Bg) || hex(p.Text) != hex(OneDark.Text) {
		t.Error("a theme that named two colours changed others")
	}
}

// The role names are opencode's, so a theme written for it mostly drops in.
func TestOpenCodesRoleNamesAreUnderstood(t *testing.T) {
	write(t, "oc", `{
		"background": "#1a1b26", "backgroundPanel": "#16161e",
		"text": "#c0caf5", "textMuted": "#9aa5ce",
		"border": "#3b4261", "borderActive": "#7aa2f7",
		"accent": "#7aa2f7", "success": "#9ece6a",
		"warning": "#e0af68", "error": "#f7768e",
		"syntaxKeyword": "#bb9af7", "syntaxString": "#9ece6a"
	}`)

	p, err := Resolve("oc")
	if err != nil {
		t.Fatalf("an opencode-shaped theme was refused: %v", err)
	}
	for _, c := range []struct {
		what string
		got  string
		want string
	}{
		{"background", hex(p.Bg), "#1a1b26"},
		{"backgroundPanel", hex(p.BgAlt), "#16161e"},
		{"text", hex(p.Text), "#c0caf5"},
		{"textMuted", hex(p.Dim), "#9aa5ce"},
		{"borderActive", hex(p.BorderOn), "#7aa2f7"},
		{"error", hex(p.Bad), "#f7768e"},
		{"syntaxKeyword", hex(p.Keyword), "#bb9af7"},
	} {
		if c.got != c.want {
			t.Errorf("%s is %s, want %s", c.what, c.got, c.want)
		}
	}
}

// A role may point at another role, which is how a theme says "the same blue".
func TestARoleCanPointAtAnother(t *testing.T) {
	write(t, "ref", `{"accent": "#7aa2f7", "borderActive": "accent", "info": "accent"}`)

	p, err := Resolve("ref")
	if err != nil {
		t.Fatal(err)
	}
	if hex(p.BorderOn) != "#7aa2f7" {
		t.Errorf("the reference did not resolve: %s", hex(p.BorderOn))
	}
}

// And one that points back at itself is a mistake with a name rather than a
// stack overflow.
func TestARoleThatPointsAtItselfIsRefused(t *testing.T) {
	write(t, "loop", `{"accent": "borderActive", "borderActive": "accent"}`)

	if _, err := Resolve("loop"); err == nil {
		t.Error("a circular reference was accepted")
	} else if !strings.Contains(err.Error(), "refers back") {
		t.Errorf("it was refused, but not clearly: %v", err)
	}
}

// Taste does not get to decide whether the words can be read. A theme whose
// prose has disappeared into the background is not a style, it is a fault —
// and one the reader will blame on this program rather than on their file.
func TestAnUnreadableThemeIsRefusedAndSaysWhy(t *testing.T) {
	write(t, "dim", `{"background": "#1e1e2e", "text": "#232334"}`)

	p, err := Resolve("dim")
	if err == nil {
		t.Fatal("a theme with invisible prose was accepted")
	}
	if !strings.Contains(err.Error(), "text is") || !strings.Contains(err.Error(), ":1") {
		t.Errorf("it was refused without saying which colour or by how much: %v", err)
	}
	// And what comes back is usable rather than half-applied.
	if hex(p.Text) != hex(OneDark.Text) {
		t.Error("the refused theme was applied anyway")
	}
}

// The hierarchy is the thing a palette is most likely to get backwards.
func TestAFlatHierarchyIsRefused(t *testing.T) {
	// Prose raised to meet the emphasis, rather than emphasis lowered — which
	// would trip an earlier floor and test something else.
	write(t, "flat", `{"text": "#e4e8ef"}`)

	if _, err := Resolve("flat"); err == nil {
		t.Error("a theme where bold is no brighter than prose was accepted")
	} else if !strings.Contains(err.Error(), "bold says nothing") {
		t.Errorf("refused, but not for the right reason: %v", err)
	}
}

// A built-in name still wins, so nobody can shadow one by accident.
func TestABuiltInNameIsNotAFile(t *testing.T) {
	write(t, "onedark", `{"background": "#ff0000"}`)

	p, err := Resolve("onedark")
	if err != nil {
		t.Fatal(err)
	}
	if hex(p.Bg) != hex(OneDark.Bg) {
		t.Errorf("a file shadowed the built-in: %s", hex(p.Bg))
	}
}

// A name with nothing behind it says so rather than starting in a colour
// nobody chose.
func TestAMissingThemeSaysSo(t *testing.T) {
	write(t, "something", `{}`)

	if _, err := Resolve("nothing-here"); err == nil {
		t.Error("a theme that does not exist was accepted")
	}
}

// hex prints a colour the way a theme file writes one.
func hex(c interface{ RGBA() (r, g, b, a uint32) }) string {
	r, g, b, _ := c.RGBA()
	const digits = "0123456789abcdef"
	out := []byte("#......")
	for i, v := range []uint32{r >> 8, g >> 8, b >> 8} {
		out[1+i*2] = digits[(v>>4)&15]
		out[2+i*2] = digits[v&15]
	}
	return string(out)
}

// The theme shipped as an example has to pass the floors it is an example of,
// and the ceiling the built-ins are held to. An example that breaks the house
// rule teaches the rule wrong.
func TestTheShippedExampleObeysTheHouseRules(t *testing.T) {
	p, err := Load(filepath.Join("..", "..", "docs", "themes", "tokyonight.json"), OneDark)
	if err != nil {
		t.Fatalf("the example does not load: %v", err)
	}
	if bad := Check(p); bad != "" {
		t.Errorf("the example is not readable: %s", bad)
	}
	if got := ratio(p.Fg, p.Bg); got > 12 {
		t.Errorf("the example's bold is %.2f:1, past the ceiling the built-ins keep", got)
	}
}
