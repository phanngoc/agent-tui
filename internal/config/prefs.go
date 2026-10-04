package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Prefs are the settings the app writes itself, from its settings page.
//
// They are kept apart from config.json on purpose. That file is the reader's:
// they write it by hand, and a program that rewrote it would drop their
// comments-by-convention, reorder their keys and fight their dotfiles repo.
// This one is ours, and nobody is expected to open it.
type Prefs struct {
	// StartDir is where a new session begins: StartLaunch, StartLast or
	// StartFixed. Empty means StartLaunch, which is what the app did before
	// there was a choice.
	StartDir string `json:"start_dir,omitempty"`
	// StartPath is the folder StartFixed opens.
	StartPath string `json:"start_path,omitempty"`
	// Defaults for a new session, chosen on the settings page. Empty means
	// what it meant before there was a page: the engine follows the one used
	// last, and the model and mode come from config.json or the flags.
	Engine string `json:"engine,omitempty"`
	Model  string `json:"model,omitempty"`
	Mode   string `json:"mode,omitempty"`
	// Theme is the palette picked on the settings page. It outranks
	// config.json's, which is the hand-written default the page started from.
	Theme string `json:"theme,omitempty"`
	// LastRoot is the folder the app was last opened on. It is what StartLast
	// reopens: the sessions saved there come back with it, and each of them
	// remembers the directory it had moved to, so the newest one picks up in
	// the very place it was left.
	LastRoot string `json:"last_root,omitempty"`
	// Effort for new sessions; empty means config.json's.
	Effort string `json:"effort,omitempty"`
	// Learn is the automatic memory: after a turn, what was worth keeping is
	// extracted and merged into memory. nil means on.
	Learn *bool `json:"learn,omitempty"`
	// LearnModel is the model that does the extracting; empty means a small,
	// cheap one, since it runs after every turn.
	LearnModel string `json:"learn_model,omitempty"`
	// LearnSkills lets learning write skills from work that showed a
	// reusable procedure. nil means on.
	LearnSkills *bool `json:"learn_skills,omitempty"`
	// GatewayAutostart starts `agent-tui serve` in the background when the
	// terminal app finds none, so the web admin always has something to talk
	// to. nil means on.
	GatewayAutostart *bool `json:"gateway_autostart,omitempty"`
	// Instructions are standing orders added to every session's system prompt.
	Instructions string `json:"instructions,omitempty"`
}

// LearnOn resolves the global switch with a project's override.
func (p Prefs) LearnOn(project ProjectSettings) bool {
	if project.Learn != nil {
		return *project.Learn
	}
	return p.Learn == nil || *p.Learn
}

// Where a new session begins.
const (
	StartLaunch = "launch" // the folder agent-tui was opened in
	StartLast   = "last"   // wherever the last session left off
	StartFixed  = "fixed"  // a folder chosen once, in the settings
)

// StartMode is StartDir with the empty value spelled out.
func (p Prefs) StartMode() string {
	switch p.StartDir {
	case StartLast, StartFixed:
		return p.StartDir
	}
	return StartLaunch
}

func prefsPath() string { return filepath.Join(DataDir(), "prefs.json") }

// LoadPrefs reads the saved settings. A missing or unreadable file means the
// defaults, which is the same thing a first run means.
func LoadPrefs() Prefs {
	var p Prefs
	b, err := os.ReadFile(prefsPath())
	if err != nil {
		return Prefs{}
	}
	if json.Unmarshal(b, &p) != nil {
		return Prefs{}
	}
	return p
}

// SavePrefs writes the settings atomically, so an interrupted write cannot
// leave a file that parses to something other than what was there.
func SavePrefs(p Prefs) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	path := prefsPath()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// RememberRoot records the folder this run was opened on, for StartLast.
//
// It reads the file again rather than writing back a copy held since startup:
// the settings page may have changed the rest of it in the meantime.
func RememberRoot(root string) {
	p := LoadPrefs()
	if p.LastRoot == root {
		return
	}
	p.LastRoot = root
	_ = SavePrefs(p)
}

// StartRoot is the folder to open on when none was asked for, given the one
// agent-tui was launched in. note is non-empty when the answer is not the
// launch folder, or when the setting could not be followed — opening somewhere
// other than where the terminal is should never go unsaid.
func (p Prefs) StartRoot(launch string) (dir, note string) {
	switch p.StartMode() {
	case StartLast:
		if p.LastRoot == "" {
			return launch, ""
		}
		if !IsDir(p.LastRoot) {
			return launch, "the last session's folder is gone (" + p.LastRoot + "); opened here instead"
		}
		if p.LastRoot == launch {
			return launch, ""
		}
		return p.LastRoot, "picked up where the last session left off: " + p.LastRoot
	case StartFixed:
		want, err := ExpandDir(p.StartPath)
		if err != nil || !IsDir(want) {
			return launch, "the start folder in /settings is not a folder (" + p.StartPath + "); opened here instead"
		}
		if want == launch {
			return launch, ""
		}
		return want, "opened in the start folder from /settings: " + want
	}
	return launch, ""
}

// ExpandDir turns what someone typed into an absolute path: a leading ~ is
// their home, and anything relative is relative to where the program runs.
func ExpandDir(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		// Abs would answer with the working directory, which is a folder
		// nobody chose.
		return "", errors.New("no folder given")
	}
	if s == "~" || strings.HasPrefix(s, "~/") || strings.HasPrefix(s, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		s = filepath.Join(home, s[1:])
	}
	return filepath.Abs(s)
}

// IsDir reports whether path names a directory that exists.
func IsDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}
