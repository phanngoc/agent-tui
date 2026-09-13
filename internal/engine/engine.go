// Package engine plugs different coding agents into the same UI.
//
// An engine turns a prompt into a stream of agent.Event values. The built-in
// one talks to the Anthropic API directly; the others drive an installed CLI
// (Claude Code, Codex, opencode) in its headless JSON mode and normalise what
// comes back. The UI never learns which is which beyond a label.
package engine

import (
	"os"
	"os/exec"
	"sort"
	"sync"

	"github.com/phanngoc/agent-tui/internal/agent"
)

// IDs of the engines this package can build.
const (
	IDAPI      = "api"
	IDClaude   = "claude"
	IDCodex    = "codex"
	IDOpenCode = "opencode"
)

// Registry holds every engine the UI can offer, in display order.
type Registry struct {
	engines []agent.Engine
	byID    map[string]agent.Engine
}

// NewRegistry builds the engine list. Detection runs once, concurrently, so a
// missing or slow binary never delays startup.
func NewRegistry(api agent.Engine, root string) *Registry {
	clis := []*CLI{
		newClaude(root),
		newCodex(root),
		newOpenCode(root),
	}

	var wg sync.WaitGroup
	for _, c := range clis {
		wg.Add(1)
		go func(c *CLI) { defer wg.Done(); c.detect() }(c)
	}
	wg.Wait()

	r := &Registry{byID: make(map[string]agent.Engine, len(clis)+1)}
	if api != nil {
		r.add(api)
	}
	for _, c := range clis {
		r.add(c)
	}
	return r
}

func (r *Registry) add(e agent.Engine) {
	r.engines = append(r.engines, e)
	r.byID[e.ID()] = e
}

// All returns every engine, available or not; the picker shows unavailable ones
// with the reason, which is more useful than hiding them.
func (r *Registry) All() []agent.Engine { return r.engines }

// Get returns the engine for id, falling back to the first available one so a
// session restored with an engine that has since been uninstalled still runs.
func (r *Registry) Get(id string) agent.Engine {
	if e, ok := r.byID[id]; ok && e.Available() {
		return e
	}
	return r.Default()
}

// Default is the first available engine.
func (r *Registry) Default() agent.Engine {
	for _, e := range r.engines {
		if e.Available() {
			return e
		}
	}
	if len(r.engines) > 0 {
		return r.engines[0]
	}
	return nil
}

// Has reports whether id names a known, available engine.
func (r *Registry) Has(id string) bool {
	e, ok := r.byID[id]
	return ok && e.Available()
}

// AvailableIDs lists the usable engine ids, sorted, for help text.
func (r *Registry) AvailableIDs() []string {
	var out []string
	for _, e := range r.engines {
		if e.Available() {
			out = append(out, e.ID())
		}
	}
	sort.Strings(out)
	return out
}

// lookPath is a variable so tests can pretend a CLI is or is not installed.
var lookPath = exec.LookPath

// NewRegistryWith builds a registry from an explicit engine list. Tests use it
// to avoid probing for CLIs that may or may not be installed on the machine.
func NewRegistryWith(engines ...agent.Engine) *Registry {
	r := &Registry{byID: make(map[string]agent.Engine, len(engines))}
	for _, e := range engines {
		if e != nil {
			r.add(e)
		}
	}
	return r
}

// executable resolves this binary, which the approval broker re-executes in
// --permission-broker mode. It is a variable so tests can point it at a real
// build instead of the test binary.
var executable = os.Executable
