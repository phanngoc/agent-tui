package engine

import (
	"context"
	"os"
	"path/filepath"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/phanngoc/agent-tui/internal/agent"
)

// apiEngine is the built-in backend: it talks to the Anthropic API directly and
// runs its tools in this process, so approvals are always real.
type apiEngine struct {
	ag    *agent.Agent
	model string
}

// NewAPI wraps the built-in agent as an engine.
func NewAPI(ag *agent.Agent, model string) agent.Engine {
	return &apiEngine{ag: ag, model: model}
}

func (e *apiEngine) ID() string    { return IDAPI }
func (e *apiEngine) Label() string { return "Built-in" }

// CanAsk is always true: the built-in agent runs its tools in this process, so
// there is nothing between it and the prompt.
func (e *apiEngine) CanAsk() bool { return true }

// Available reports whether a credential can actually be found. Offering an
// engine that fails on the first prompt is worse than greying it out, so this
// checks rather than assumes.
func (e *apiEngine) Available() bool { _, ok := credentials(); return ok }

func (e *apiEngine) Detail() string {
	source, ok := credentials()
	if !ok {
		return source
	}
	return e.model + " · " + source + " · honours every mode"
}

// credentials mirrors how the SDK resolves an Anthropic credential and returns
// either the source it found or what to do about it being missing.
func credentials() (string, bool) {
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		return "ANTHROPIC_API_KEY", true
	}
	if os.Getenv("ANTHROPIC_AUTH_TOKEN") != "" {
		return "ANTHROPIC_AUTH_TOKEN", true
	}
	if profilePath() != "" {
		return "ant profile", true
	}
	return "no credentials: set ANTHROPIC_API_KEY or run `ant auth login`", false
}

// profilePath returns the SDK's credential file, if one exists.
func profilePath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	for _, name := range []string{"config.json", "credentials.json", "auth.json"} {
		p := filepath.Join(base, "anthropic", name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// Run continues the session's own SDK history when there is one, and otherwise
// rebuilds it from the persisted transcript.
func (e *apiEngine) Run(ctx context.Context, t agent.Turn, out chan<- agent.Event) {
	hist, ok := t.State.([]anthropic.MessageParam)
	if !ok {
		// History already ends with this turn's prompt, so replaying it is the
		// whole conversation. Appending the prompt again would send it twice.
		e.ag.Run(ctx, agent.Replay(t.History), t.Mode, t.Model, out)
		return
	}
	if blocks := agent.UserBlocks(t.Prompt, t.Files); len(blocks) > 0 {
		hist = append(hist, anthropic.NewUserMessage(blocks...))
	}
	e.ag.Run(ctx, hist, t.Mode, t.Model, out)
}
