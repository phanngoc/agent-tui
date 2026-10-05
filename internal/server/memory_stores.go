package server

import (
	"path/filepath"
	"strings"

	"github.com/phanngoc/agent-tui/internal/config"
	"github.com/phanngoc/agent-tui/internal/memory"
)

// storeRef is a memory store and the project it belongs to, as a page shows
// it. A project store's folder is named after its project's path; the path
// itself is recovered from the places sessions ran.
type storeRef struct {
	Store *memory.Store
	Root  string // empty for global, or for a project no session names any more
	Name  string
}

// knownRoots maps a project's folder name in the data directory to its path.
func (s *Server) knownRoots() map[string]string {
	out := map[string]string{}
	add := func(root string) {
		if root != "" {
			out[strings.ToLower(config.ProjectSlug(root))] = root
		}
	}
	for _, sum := range s.summaries() {
		add(sum.Root)
	}
	add(config.LoadPrefs().LastRoot)
	for _, p := range s.Hub.Peers() {
		add(p.Root)
	}
	return out
}

// memoryStores lists the stores a request means: the global one, and either
// one project's or, with all, every project's there is.
func (s *Server) memoryStores(root string, all bool) []storeRef {
	refs := []storeRef{{Store: memory.For("").Global, Name: "global"}}
	if !all {
		if root != "" {
			refs = append(refs, storeRef{Store: memory.For(root).Project, Root: root, Name: filepath.Base(root)})
		}
		return refs
	}
	roots := s.knownRoots()
	for _, dir := range memory.ProjectDirs() {
		st, ok := memory.AtDir(dir)
		if !ok {
			continue
		}
		slug := filepath.Base(filepath.Dir(dir))
		ref := storeRef{Store: st, Root: roots[strings.ToLower(slug)], Name: slug}
		if ref.Root != "" {
			ref.Name = filepath.Base(ref.Root)
		}
		refs = append(refs, ref)
	}
	return refs
}
