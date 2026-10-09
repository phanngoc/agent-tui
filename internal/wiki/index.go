package wiki

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// indexOrder is the order index.md lists the types in.
var indexOrder = []struct{ typ, heading string }{
	{TypeSource, "Sources"}, {TypeEntity, "Entities"}, {TypeConcept, "Concepts"},
	{TypeComparison, "Comparisons"}, {TypeSynthesis, "Synthesis"},
}

// rebuildIndex writes index.md from the pages: every page, by type, by title,
// with its description. No model is asked; it is a table of contents.
func (w *Wiki) rebuildIndex() {
	pages := w.Pages()
	by := map[string][]Page{}
	for _, p := range pages {
		t := p.Type
		known := false
		for _, o := range indexOrder {
			known = known || o.typ == t
		}
		if !known {
			t = "other"
		}
		by[t] = append(by[t], p)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Index\n\n%d pages. Rebuilt %s.\n", len(pages), time.Now().Format("2006-01-02 15:04"))
	sections := append(indexOrder[:len(indexOrder):len(indexOrder)], struct{ typ, heading string }{"other", "Other"})
	for _, s := range sections {
		ps := by[s.typ]
		if len(ps) == 0 {
			continue
		}
		sort.Slice(ps, func(i, j int) bool { return strings.ToLower(ps[i].Title) < strings.ToLower(ps[j].Title) })
		fmt.Fprintf(&b, "\n## %s\n\n", s.heading)
		for _, p := range ps {
			fmt.Fprintf(&b, "- [[%s]]", p.Title)
			if d := p.Description; d != "" {
				b.WriteString(" — " + d)
			}
			b.WriteString("\n")
		}
	}
	_ = os.MkdirAll(w.Dir, 0o755)
	_ = writeAtomic(filepath.Join(w.Dir, "index.md"), []byte(b.String()))
}

// RebuildIndex rewrites index.md, for a wiki edited outside this package.
func (w *Wiki) RebuildIndex() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.rebuildIndex()
}

// appendLog adds an entry to log.md under today's date, newest first.
func (w *Wiki) appendLog(what string, pages []string) {
	file := filepath.Join(w.Dir, "log.md")
	old, _ := os.ReadFile(file)
	body := strings.TrimPrefix(strings.TrimSpace(string(old)), "# Log")
	body = strings.TrimSpace(body)
	day := "## " + time.Now().Format("2006-01-02")
	entry := "- " + time.Now().Format("15:04") + " " + what
	if len(pages) > 0 {
		shown := pages
		if len(shown) > 12 {
			shown = shown[:12]
		}
		entry += " — " + strings.Join(shown, ", ")
		if len(pages) > len(shown) {
			entry += fmt.Sprintf(" and %d more", len(pages)-len(shown))
		}
	}
	if strings.HasPrefix(body, day) {
		body = day + "\n\n" + entry + "\n" + strings.TrimLeft(strings.TrimPrefix(body, day), "\n")
	} else {
		body = day + "\n\n" + entry + "\n\n" + body
	}
	_ = os.MkdirAll(w.Dir, 0o755)
	_ = writeAtomic(file, []byte("# Log\n\n"+strings.TrimSpace(body)+"\n"))
}

// Lint is what a look over the wiki found: links to nowhere, pages nothing
// links to, pages that name no source, and raw files an ingest gave up on.
type Lint struct {
	Broken  map[string][]string `json:"broken"`
	Orphans []string            `json:"orphans"`
	NoSrc   []string            `json:"no_source"`
	Failed  map[string]string   `json:"failed"`
}

// Lint checks the wiki without a model.
func (w *Wiki) Lint() Lint {
	g := NewGraph(w.Pages())
	l := Lint{Broken: g.Broken, Orphans: g.Orphans(), Failed: map[string]string{}}
	for id, p := range g.Pages {
		if len(p.Sources) == 0 {
			l.NoSrc = append(l.NoSrc, id)
		}
	}
	sort.Strings(l.NoSrc)
	for name, s := range w.State().Sources {
		if s.Status == "failed" {
			l.Failed[name] = s.Error
		}
	}
	return l
}
