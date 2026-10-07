package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ProjectDir is the folder inside a project that holds what belongs to that
// project and is meant to travel with it: its skills, its MCP servers and its
// settings. It sits in the repository, like .claude or .vscode, so a team can
// commit it.
func ProjectDir(root string) string { return filepath.Join(root, ".agent-tui") }

// ProjectDataDir is where this machine keeps what it learned about a project:
// the memory. It is kept out of the repository on purpose — what one person's
// agent noticed about them is not something to push to a shared remote — and
// it is keyed by the project's path, the way sessions already are.
func ProjectDataDir(root string) string {
	return filepath.Join(DataDir(), "projects", ProjectSlug(root))
}

// ProjectSlug turns a path into one folder name: every separator, colon and
// space becomes a dash, so C:\code\x and /home/me/x stay apart and readable.
func ProjectSlug(root string) string {
	r := strings.NewReplacer(`\`, "-", "/", "-", ":", "-", " ", "-", ".", "-")
	s := r.Replace(filepath.Clean(root))
	if s == "" {
		return "-"
	}
	return s
}

// ProjectSettings override Prefs for one project. Every field is optional:
// empty means "whatever the global setting says", so a project file only has
// to name what is different about it.
type ProjectSettings struct {
	Engine string `json:"engine,omitempty"`
	Model  string `json:"model,omitempty"`
	Mode   string `json:"mode,omitempty"`
	Effort string `json:"effort,omitempty"`
	// Learn turns the automatic memory on or off for this project. nil
	// follows the global setting.
	Learn *bool `json:"learn,omitempty"`
	// DisabledSkills and DisabledMCP switch off things defined globally for
	// this project only, without deleting them for every other one.
	DisabledSkills []string `json:"disabled_skills,omitempty"`
	DisabledMCP    []string `json:"disabled_mcp,omitempty"`
	// Instructions are standing orders for the agent in this project, added to
	// its system prompt — the project's own AGENTS.md, edited from the admin.
	Instructions string `json:"instructions,omitempty"`
	// AllowedDirs are folders besides this project that auto mode may change
	// without asking when working on it: the sibling repositories it is
	// developed alongside.
	AllowedDirs []string `json:"allowed_dirs,omitempty"`
}

func projectSettingsPath(root string) string {
	return filepath.Join(ProjectDir(root), "settings.json")
}

// LoadProjectSettings reads root/.agent-tui/settings.json. A missing file is
// the empty settings, which override nothing.
func LoadProjectSettings(root string) ProjectSettings {
	var s ProjectSettings
	b, err := os.ReadFile(projectSettingsPath(root))
	if err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

// SaveProjectSettings writes the file, creating .agent-tui on demand.
func SaveProjectSettings(root string, s ProjectSettings) error {
	if root == "" {
		return errors.New("no project")
	}
	return WriteJSON(projectSettingsPath(root), s)
}

// WriteJSON writes v as indented JSON through a temporary file and a rename,
// so a reader never sees half a file.
func WriteJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Disabled reports whether name is in list.
func Disabled(list []string, name string) bool {
	for _, n := range list {
		if n == name {
			return true
		}
	}
	return false
}
