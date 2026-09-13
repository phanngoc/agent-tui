package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phanngoc/agent-tui/internal/agent"
)

// TestAPIEngineReportsMissingCredentials guards an honesty bug: the picker used
// to claim it was "using your ant profile" on a machine with no credential at
// all, and then failed on the first prompt.
func TestAPIEngineReportsMissingCredentials(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // no profile here

	e := NewAPI(nil, "claude-opus-5")
	if e.Available() {
		t.Error("the engine claims to be usable with no credentials")
	}
	if got := e.Detail(); !strings.Contains(got, "ANTHROPIC_API_KEY") {
		t.Errorf("detail should say how to fix it, got %q", got)
	}
	if got := e.Detail(); strings.Contains(got, "asks before acting") {
		t.Errorf("an unusable engine should not advertise behaviour: %q", got)
	}
}

func TestAPIEngineFindsEachCredentialSource(t *testing.T) {
	t.Run("api key", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "sk-test")
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())

		e := NewAPI(nil, "m")
		if !e.Available() {
			t.Fatal("an API key was not recognised")
		}
		if got := e.Detail(); !strings.Contains(got, "ANTHROPIC_API_KEY") {
			t.Errorf("detail = %q", got)
		}
	})

	t.Run("profile on disk", func(t *testing.T) {
		cfg := t.TempDir()
		if err := os.MkdirAll(filepath.Join(cfg, "anthropic"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cfg, "anthropic", "config.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("ANTHROPIC_API_KEY", "")
		t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
		t.Setenv("XDG_CONFIG_HOME", cfg)

		e := NewAPI(nil, "m")
		if !e.Available() {
			t.Fatal("a profile on disk was not recognised")
		}
		if got := e.Detail(); !strings.Contains(got, "ant profile") {
			t.Errorf("detail = %q", got)
		}
	})
}

// TestRegistryFallsBackToAWorkingEngine makes sure a session is never handed an
// engine that cannot run.
func TestRegistryFallsBackToAWorkingEngine(t *testing.T) {
	r := NewRegistryWith(
		stubEngine{id: IDAPI, ok: false},
		stubEngine{id: IDClaude, ok: true},
	)

	if got := r.Default(); got.ID() != IDClaude {
		t.Errorf("default engine = %q, want the one that works", got.ID())
	}
	if got := r.Get(IDAPI); got.ID() != IDClaude {
		t.Errorf("asking for an unusable engine returned %q, want a working fallback", got.ID())
	}
	if r.Has(IDAPI) {
		t.Error("an unusable engine should not report as available")
	}
	if ids := r.AvailableIDs(); len(ids) != 1 || ids[0] != IDClaude {
		t.Errorf("available ids = %v", ids)
	}
	// Unavailable engines still appear in the picker, with their reason.
	if len(r.All()) != 2 {
		t.Errorf("the picker should still list every engine, got %d", len(r.All()))
	}
}

type stubEngine struct {
	id string
	ok bool
}

func (s stubEngine) ID() string      { return s.id }
func (s stubEngine) Label() string   { return s.id }
func (s stubEngine) Detail() string  { return "" }
func (s stubEngine) Available() bool { return s.ok }
func (s stubEngine) CanAsk() bool    { return false }
func (s stubEngine) Run(ctx context.Context, t agent.Turn, out chan<- agent.Event) {
	close(out)
}
