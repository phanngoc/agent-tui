package theme

import (
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

// A theme is data, not code.
//
// Three palettes were compiled in, which meant the only way to have a fourth
// was to be the person who builds this. That is the wrong shape for the one
// part of a tool that is entirely a matter of taste — and it is the shape
// opencode got right: a theme is a JSON file in a directory, and the roles it
// names say what a colour is *for* rather than what it is.
//
// The role names here are opencode's, so a theme written for it mostly drops
// straight in. Where this program knows something opencode's two text tones do
// not cover — it reads prose at one weight, emphasis at another, and metadata
// at a third — the extra roles are its own and are optional.
//
// Anything a file leaves out keeps the built-in value. That is what makes a
// four-line theme worth writing: you say the three colours you actually care
// about and the rest stays measured.

// File is a theme as it is written on disk.
//
// Every field is a colour reference: a hex string, an ANSI index, or the name
// of another role. Missing fields are not errors — they are the parts you did
// not want to change.
type File struct {
	Name string `json:"name,omitempty"`
	// Where opencode and this program agree.
	Background        string `json:"background,omitempty"`
	BackgroundPanel   string `json:"backgroundPanel,omitempty"`
	BackgroundElement string `json:"backgroundElement,omitempty"`
	Text              string `json:"text,omitempty"`
	TextMuted         string `json:"textMuted,omitempty"`
	Border            string `json:"border,omitempty"`
	BorderActive      string `json:"borderActive,omitempty"`
	BorderSubtle      string `json:"borderSubtle,omitempty"`
	Accent            string `json:"accent,omitempty"`
	Error             string `json:"error,omitempty"`
	Warning           string `json:"warning,omitempty"`
	Success           string `json:"success,omitempty"`
	Info              string `json:"info,omitempty"`

	// This program reads at three weights rather than two: the prose of an
	// answer, the emphasis inside it, and the notes beside it. A theme that
	// names only text and textMuted gets the built-in's spacing between them.
	TextStrong string `json:"textStrong,omitempty"`
	TextSubtle string `json:"textSubtle,omitempty"`
	Selection  string `json:"selection,omitempty"`

	DiffAddedBg   string `json:"diffAddedBg,omitempty"`
	DiffRemovedBg string `json:"diffRemovedBg,omitempty"`

	SyntaxComment     string `json:"syntaxComment,omitempty"`
	SyntaxKeyword     string `json:"syntaxKeyword,omitempty"`
	SyntaxFunction    string `json:"syntaxFunction,omitempty"`
	SyntaxString      string `json:"syntaxString,omitempty"`
	SyntaxNumber      string `json:"syntaxNumber,omitempty"`
	SyntaxType        string `json:"syntaxType,omitempty"`
	SyntaxPunctuation string `json:"syntaxPunctuation,omitempty"`
}

// Load reads a theme file and lays it over a base palette.
//
// Over rather than instead of: a file says what it wants changed, and the rest
// stays what it was. A theme that sets three colours is a theme that has
// thought about three colours, which is more often the truth than a file with
// forty in it.
func Load(path string, base Palette) (Palette, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return base, err
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return base, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return f.Apply(base)
}

// Apply lays a file over a palette, resolving references as it goes.
func (f File) Apply(base Palette) (Palette, error) {
	p := base
	seen := map[string]bool{}

	// resolve turns one field into a colour, following a name to the role it
	// points at. A reference that goes round in a circle is a mistake worth
	// naming rather than a stack overflow.
	var resolve func(field, spec string) (color.Color, error)
	resolve = func(field, spec string) (color.Color, error) {
		spec = strings.TrimSpace(spec)
		switch {
		case spec == "", spec == "none":
			return nil, nil
		case strings.HasPrefix(spec, "#"):
			if len(spec) != 4 && len(spec) != 7 {
				return nil, fmt.Errorf("%s: %q is not a hex colour", field, spec)
			}
			return lipgloss.Color(spec), nil
		}
		if n, err := strconv.Atoi(spec); err == nil {
			if n < 0 || n > 255 {
				return nil, fmt.Errorf("%s: ANSI %d is outside 0-255", field, n)
			}
			return lipgloss.ANSIColor(n), nil
		}
		// A name: the role it points at, in this file.
		if seen[spec] {
			return nil, fmt.Errorf("%s: %q refers back to itself", field, spec)
		}
		seen[spec] = true
		defer delete(seen, spec)
		next, ok := f.byName(spec)
		if !ok {
			return nil, fmt.Errorf("%s: there is no role called %q", field, spec)
		}
		return resolve(spec, next)
	}

	// The order is the order of the struct, so an error names the first field
	// that is wrong rather than whichever the map happened to reach first.
	for _, r := range []struct {
		field string
		spec  string
		set   *color.Color
	}{
		{"background", f.Background, &p.Bg},
		{"backgroundPanel", f.BackgroundPanel, &p.BgAlt},
		{"backgroundElement", f.BackgroundElement, &p.Row},
		{"text", f.Text, &p.Text},
		{"textStrong", f.TextStrong, &p.Fg},
		{"textMuted", f.TextMuted, &p.Dim},
		{"textSubtle", f.TextSubtle, &p.Faint},
		{"border", f.Border, &p.Border},
		{"borderActive", f.BorderActive, &p.BorderOn},
		{"borderSubtle", f.BorderSubtle, &p.Sel},
		{"selection", f.Selection, &p.Sel},
		{"accent", f.Accent, &p.Accent},
		{"success", f.Success, &p.Good},
		{"warning", f.Warning, &p.Warn},
		{"error", f.Error, &p.Bad},
		{"info", f.Info, &p.Accent},
		{"diffAddedBg", f.DiffAddedBg, &p.AddBg},
		{"diffRemovedBg", f.DiffRemovedBg, &p.DelBg},
		{"syntaxComment", f.SyntaxComment, &p.Comment},
		{"syntaxKeyword", f.SyntaxKeyword, &p.Keyword},
		{"syntaxFunction", f.SyntaxFunction, &p.Func},
		{"syntaxString", f.SyntaxString, &p.String},
		{"syntaxNumber", f.SyntaxNumber, &p.Number},
		{"syntaxType", f.SyntaxType, &p.Type},
		{"syntaxPunctuation", f.SyntaxPunctuation, &p.Punct},
	} {
		c, err := resolve(r.field, r.spec)
		if err != nil {
			return base, err
		}
		if c != nil {
			*r.set = c
		}
	}
	return p, nil
}

func (f File) byName(name string) (string, bool) {
	switch name {
	case "background":
		return f.Background, true
	case "backgroundPanel":
		return f.BackgroundPanel, true
	case "backgroundElement":
		return f.BackgroundElement, true
	case "text":
		return f.Text, true
	case "textStrong":
		return f.TextStrong, true
	case "textMuted":
		return f.TextMuted, true
	case "textSubtle":
		return f.TextSubtle, true
	case "border":
		return f.Border, true
	case "borderActive":
		return f.BorderActive, true
	case "borderSubtle":
		return f.BorderSubtle, true
	case "accent":
		return f.Accent, true
	case "error":
		return f.Error, true
	case "warning":
		return f.Warning, true
	case "success":
		return f.Success, true
	case "info":
		return f.Info, true
	}
	return "", false
}

// Dir is where theme files live.
func Dir() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "agent-tui", "themes")
}

// Installed lists the theme files on this machine, by the name you would use
// to ask for one.
func Installed() []string {
	dir := Dir()
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			out = append(out, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	sort.Strings(out)
	return out
}

// ErrUnreadable means a theme was loaded and would not have been legible.
var ErrUnreadable = errors.New("theme is not readable")

// Resolve turns a theme name into a palette: a built-in, or a file.
//
// A file that fails the floors this package holds every palette to is refused
// rather than shown. A theme is taste, and the one thing taste does not get to
// decide is whether the words can be read — a UI whose comment colour has
// disappeared into the background is not a style, it is a fault, and it is one
// the reader will blame on the program rather than on their file.
func Resolve(name string) (Palette, error) {
	if name == "" {
		return OneDark, nil
	}
	for _, n := range Names {
		if n == name {
			return ByName(name), nil
		}
	}
	dir := Dir()
	if dir == "" {
		return OneDark, fmt.Errorf("theme %q: nowhere to look for theme files", name)
	}
	p, err := Load(filepath.Join(dir, name+".json"), OneDark)
	if err != nil {
		return OneDark, err
	}
	if bad := Check(p); bad != "" {
		return OneDark, fmt.Errorf("%w: %s", ErrUnreadable, bad)
	}
	return p, nil
}

// Check reports the first way a palette would be unreadable, or "".
//
// The same floors the built-in palettes are held to by this package's tests,
// applied at the moment a file is loaded — because a guarantee that only
// covers the colours that ship is not a guarantee about the program.
func Check(p Palette) string {
	for _, c := range []struct {
		name string
		fg   color.Color
		bg   color.Color
		min  float64
	}{
		{"text", p.Text, p.Bg, 7},
		{"textStrong", p.Fg, p.Bg, 7},
		{"textMuted", p.Dim, p.Bg, 7},
		{"textSubtle", p.Faint, p.Bg, 4.5},
		{"accent", p.Accent, p.Bg, 4.5},
		{"success", p.Good, p.Bg, 4.5},
		{"warning", p.Warn, p.Bg, 4.5},
		{"error", p.Bad, p.Bg, 4.5},
		{"syntaxComment", p.Comment, p.Bg, 4.5},
		{"syntaxKeyword", p.Keyword, p.Bg, 4.5},
		{"syntaxString", p.String, p.Bg, 4.5},
		{"syntaxNumber", p.Number, p.Bg, 4.5},
		{"syntaxFunction", p.Func, p.Bg, 4.5},
		{"syntaxType", p.Type, p.Bg, 4.5},
		{"text on the selection", p.Fg, p.Sel, 4.5},
	} {
		if got := ratio(c.fg, c.bg); got < c.min {
			return fmt.Sprintf("%s is %.2f:1 against the background, and needs %.1f:1",
				c.name, got, c.min)
		}
	}
	// And the hierarchy, which is the thing a palette is most likely to get
	// backwards: emphasis brighter than prose, prose brighter than the notes.
	if ratio(p.Fg, p.Bg) <= ratio(p.Text, p.Bg) {
		return "textStrong is no brighter than text, so bold says nothing"
	}
	if ratio(p.Text, p.Bg) <= ratio(p.Dim, p.Bg) {
		return "text is no brighter than textMuted, so the prose and the notes read alike"
	}
	return ""
}
