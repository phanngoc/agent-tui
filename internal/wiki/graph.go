package wiki

import (
	"path"
	"sort"
	"strings"
)

// Graph is the pages and the links between them, worked out from the files.
type Graph struct {
	Pages map[string]Page
	Out   map[string][]string // id -> ids it links to
	In    map[string][]string // id -> ids that link to it
	// Broken is every link that names no page, by the page it is on.
	Broken map[string][]string

	bySlug map[string]string // slug of a file name or a title -> id
}

// NewGraph links a set of pages.
func NewGraph(pages []Page) *Graph {
	g := &Graph{Pages: map[string]Page{}, Out: map[string][]string{}, In: map[string][]string{},
		Broken: map[string][]string{}, bySlug: map[string]string{}}
	for _, p := range pages {
		g.Pages[p.ID] = p
	}
	// File names first, so a title that slugs to another page's file name
	// does not take it over.
	for _, p := range pages {
		g.bySlug[path.Base(p.ID)] = p.ID
	}
	for _, p := range pages {
		if s := Slugify(p.Title); g.bySlug[s] == "" {
			g.bySlug[s] = p.ID
		}
	}
	for _, p := range pages {
		for _, l := range p.Links() {
			to, ok := g.Resolve(l)
			if !ok {
				g.Broken[p.ID] = append(g.Broken[p.ID], l)
				continue
			}
			if to == p.ID {
				continue
			}
			g.Out[p.ID] = union(g.Out[p.ID], []string{to})
			g.In[to] = union(g.In[to], []string{p.ID})
		}
	}
	return g
}

// Resolve finds the page a reference means: an id, a file name, or a title,
// as a [[wikilink]] or an agent would write it.
func (g *Graph) Resolve(ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(ref, "pages/"), "/"), ".md")
	if _, ok := g.Pages[ref]; ok {
		return ref, true
	}
	if id, ok := g.bySlug[Slugify(path.Base(ref))]; ok {
		return id, true
	}
	if id, ok := g.bySlug[Slugify(ref)]; ok {
		return id, true
	}
	return "", false
}

// Neighbour is a page linked to another, and which way.
type Neighbour struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Dir   string `json:"dir"` // out, in or both
}

// Related lists a page's neighbours, the best connected first.
func (g *Graph) Related(id string, limit int) []Neighbour {
	dir := map[string]string{}
	for _, o := range g.Out[id] {
		dir[o] = "out"
	}
	for _, i := range g.In[id] {
		if dir[i] == "out" {
			dir[i] = "both"
		} else {
			dir[i] = "in"
		}
	}
	out := make([]Neighbour, 0, len(dir))
	for n, d := range dir {
		out = append(out, Neighbour{ID: n, Title: g.Pages[n].Title, Dir: d})
	}
	deg := func(id string) int { return len(g.Out[id]) + len(g.In[id]) }
	sort.Slice(out, func(i, j int) bool {
		if deg(out[i].ID) != deg(out[j].ID) {
			return deg(out[i].ID) > deg(out[j].ID)
		}
		return out[i].ID < out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// neighbours is every page one link away, either way.
func (g *Graph) neighbours(id string) []string {
	return union(append([]string(nil), g.Out[id]...), g.In[id])
}

// Orphans are the pages nothing links to, sources aside: a source page is
// reached from the index, not from other pages.
func (g *Graph) Orphans() []string {
	var out []string
	for id, p := range g.Pages {
		if len(g.In[id]) == 0 && p.Type != TypeSource {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}
