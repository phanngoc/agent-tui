package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStartRoot(t *testing.T) {
	launch, other := t.TempDir(), t.TempDir()
	gone := filepath.Join(other, "deleted")

	for _, tc := range []struct {
		name     string
		p        Prefs
		want     string
		wantNote bool
	}{
		{"nothing set opens where launched", Prefs{}, launch, false},
		{"launch opens where launched", Prefs{StartDir: StartLaunch, LastRoot: other}, launch, false},
		{"last reopens the last folder", Prefs{StartDir: StartLast, LastRoot: other}, other, true},
		{"last with no history yet", Prefs{StartDir: StartLast}, launch, false},
		{"last that is the launch folder says nothing", Prefs{StartDir: StartLast, LastRoot: launch}, launch, false},
		{"last that was deleted falls back, out loud", Prefs{StartDir: StartLast, LastRoot: gone}, launch, true},
		{"fixed opens the folder", Prefs{StartDir: StartFixed, StartPath: other}, other, true},
		{"fixed with nothing typed falls back", Prefs{StartDir: StartFixed}, launch, true},
		{"fixed that was deleted falls back", Prefs{StartDir: StartFixed, StartPath: gone}, launch, true},
		{"an unknown mode is launch", Prefs{StartDir: "sideways", LastRoot: other}, launch, false},
	} {
		got, note := tc.p.StartRoot(launch)
		if got != tc.want || (note != "") != tc.wantNote {
			t.Errorf("%s: got %q note=%q, want %q note=%v", tc.name, got, note, tc.want, tc.wantNote)
		}
	}
}

func TestExpandDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory here")
	}
	if got, _ := ExpandDir("~"); got != home {
		t.Errorf("~ = %q, want %q", got, home)
	}
	if got, _ := ExpandDir("~/projects"); got != filepath.Join(home, "projects") {
		t.Errorf("~/projects = %q", got)
	}
	if _, err := ExpandDir("   "); err == nil {
		t.Error("an empty folder should not quietly become the working directory")
	}
	if got, _ := ExpandDir("rel"); !filepath.IsAbs(got) {
		t.Errorf("relative path stayed relative: %q", got)
	}
}

// TestRememberRootKeepsTheSetting: recording the last folder on the way out
// must not undo a choice made on the settings page during the run.
func TestRememberRootKeepsTheSetting(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := SavePrefs(Prefs{StartDir: StartFixed, StartPath: "/work"}); err != nil {
		t.Fatal(err)
	}
	RememberRoot("/somewhere")
	p := LoadPrefs()
	if p.StartDir != StartFixed || p.StartPath != "/work" || p.LastRoot != "/somewhere" {
		t.Errorf("got %+v", p)
	}
}

func TestLoadPrefsToleratesJunk(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if p := LoadPrefs(); p.StartMode() != StartLaunch {
		t.Errorf("a first run should open where launched, got %q", p.StartMode())
	}
	if err := os.WriteFile(prefsPath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if p := LoadPrefs(); p != (Prefs{}) {
		t.Errorf("a broken file should mean the defaults, got %+v", p)
	}
}
