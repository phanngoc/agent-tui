package wiki

import (
	"math"
	"sort"

	"github.com/phanngoc/agent-tui/internal/memory"
)

// Search options, MemoryKnowledge's defaults.
const (
	DefaultLimit  = 10
	DefaultDecay  = 0.5
	DefaultMin    = 0.1
	expansionCap  = 200
	titleWeight   = 5.0
	maxHops       = 5
	relatedListed = 10
)

// Query is a search.
type Query struct {
	Text  string
	Limit int
	// Hops widens the search along links: a page linked from a match scores
	// the match's score times Decay, and so on Hops links out. Pages that
	// fall under Min are left out.
	Hops  int
	Decay float64
	Min   float64
}

// Result is one page found.
type Result struct {
	ID      string      `json:"id"`
	Title   string      `json:"title"`
	Type    string      `json:"type"`
	Snippet string      `json:"snippet"`
	Score   float64     `json:"score"`
	Hop     int         `json:"hop"`
	Via     string      `json:"via,omitempty"` // the page it was reached from
	Related []Neighbour `json:"related,omitempty"`
}

// Search ranks the wiki's pages against a query.
func (w *Wiki) Search(q Query) []Result { return NewGraph(w.Pages()).Search(q) }

// Search ranks pages by BM25 over their titles (weighted five times) and
// bodies, scaled so the best match is 1, then, with Hops, follows links out
// from the matches.
func (g *Graph) Search(q Query) []Result {
	if q.Limit <= 0 {
		q.Limit = DefaultLimit
	}
	if q.Decay <= 0 || q.Decay > 1 {
		q.Decay = DefaultDecay
	}
	if q.Min <= 0 {
		q.Min = DefaultMin
	}
	q.Hops = max(0, min(q.Hops, maxHops))

	seeds := g.bm25(q.Text)
	if len(seeds) == 0 {
		return nil
	}
	pool := q.Limit
	if q.Hops > 0 {
		pool *= 2
	}
	if len(seeds) > pool {
		seeds = seeds[:pool]
	}
	best := map[string]Result{}
	for _, s := range seeds {
		best[s.ID] = s
	}
	if q.Hops > 0 {
		frontier := seeds
		visited := len(best)
		for hop := 1; hop <= q.Hops && len(frontier) > 0 && visited < expansionCap; hop++ {
			var next []Result
			for _, f := range frontier {
				for _, n := range g.neighbours(f.ID) {
					s := f.Score * q.Decay
					if s < q.Min {
						continue
					}
					if old, ok := best[n]; ok && old.Score >= s {
						continue
					}
					if _, ok := best[n]; !ok {
						visited++
					}
					p := g.Pages[n]
					r := Result{ID: n, Title: p.Title, Type: p.Type, Snippet: p.Snippet(), Score: s, Hop: hop, Via: f.Title}
					best[n] = r
					next = append(next, r)
					if visited >= expansionCap {
						break
					}
				}
			}
			frontier = next
		}
	}
	out := make([]Result, 0, len(best))
	for _, r := range best {
		if r.Score >= q.Min || r.Hop == 0 {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > q.Limit {
		out = out[:q.Limit]
	}
	for i := range out {
		out[i].Related = g.Related(out[i].ID, relatedListed)
	}
	return out
}

// bm25 scores every page with a term in common, best first, scaled to 0..1.
func (g *Graph) bm25(text string) []Result {
	terms := uniqTokens(text)
	if len(terms) == 0 || len(g.Pages) == 0 {
		return nil
	}
	type doc struct {
		p           Page
		title, body map[string]int
		tlen, blen  int
	}
	docs := make([]doc, 0, len(g.Pages))
	df := map[string]int{}
	var tTotal, bTotal int
	for _, p := range g.Pages {
		d := doc{p: p, title: map[string]int{}, body: map[string]int{}}
		tt, bt := memory.Tokens(p.Title), memory.Tokens(p.Description+" "+p.Body)
		d.tlen, d.blen = len(tt), len(bt)
		tTotal += d.tlen
		bTotal += d.blen
		seen := map[string]bool{}
		for _, t := range tt {
			d.title[t]++
			seen[t] = true
		}
		for _, t := range bt {
			d.body[t]++
			seen[t] = true
		}
		for t := range seen {
			df[t]++
		}
		docs = append(docs, d)
	}
	n := float64(len(docs))
	tAvg, bAvg := float64(max(tTotal, 1))/n, float64(max(bTotal, 1))/n
	const k1, b = 1.2, 0.75
	field := func(f, l int, avg float64) float64 {
		ff := float64(f)
		return ff * (k1 + 1) / (ff + k1*(1-b+b*float64(l)/avg))
	}
	var out []Result
	top := 0.0
	for _, d := range docs {
		var score float64
		for _, t := range terms {
			if df[t] == 0 {
				continue
			}
			idf := math.Log(1 + (n-float64(df[t])+0.5)/(float64(df[t])+0.5))
			if f := d.title[t]; f > 0 {
				score += titleWeight * idf * field(f, d.tlen, tAvg)
			}
			if f := d.body[t]; f > 0 {
				score += idf * field(f, d.blen, bAvg)
			}
		}
		if score <= 0 {
			continue
		}
		top = math.Max(top, score)
		out = append(out, Result{ID: d.p.ID, Title: d.p.Title, Type: d.p.Type, Snippet: d.p.Snippet(), Score: score})
	}
	for i := range out {
		out[i].Score /= top
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// uniqTokens tokenizes a query the way memory does, keeping each word once.
func uniqTokens(s string) []string {
	return union(nil, memory.Tokens(s))
}
