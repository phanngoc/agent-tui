package config

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// The folders auto may change besides the project: the temp folders, the
// project's worktrees, and what config.json and the project's settings add —
// expanded, absolute, and each once.
func TestAllowedDirs(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SIBLING", filepath.Join(home, "sibling"))
	root := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON(filepath.Join(cfg, "agent-tui", "config.json"),
		map[string]any{"allowed_dirs": []string{"~/code/other", "$SIBLING", "relative/ignored", os.TempDir()}}); err != nil {
		t.Fatal(err)
	}
	if err := SaveProjectSettings(root, ProjectSettings{AllowedDirs: []string{filepath.Join(home, "api")}}); err != nil {
		t.Fatal(err)
	}

	got := AllowedDirs(root)
	for _, want := range []string{
		filepath.Clean(os.TempDir()),
		"/tmp",
		root + ".worktrees",
		filepath.Join(home, "code", "other"),
		filepath.Join(home, "sibling"),
		filepath.Join(home, "api"),
	} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %s in %q", want, got)
		}
	}
	seen := map[string]bool{}
	for _, d := range got {
		k := d
		if runtime.GOOS == "windows" {
			k = strings.ToLower(d)
		}
		if seen[k] {
			t.Errorf("%s listed twice", d)
		}
		seen[k] = true
		if strings.Contains(d, "relative") {
			t.Errorf("a relative folder was kept: %s", d)
		}
	}
	// --add-dir gets only what is there.
	for _, d := range ExistingDirs(got) {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			t.Errorf("ExistingDirs kept %s", d)
		}
	}
	if slices.Contains(ExistingDirs(got), root+".worktrees") {
		t.Error("ExistingDirs kept a worktrees folder that was never made")
	}
}
