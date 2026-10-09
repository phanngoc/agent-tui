package kit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/wiki"
)

// wikiContext tells the agent the wiki exists and what it covers. It is the
// same every turn — TencentDB's proxy stopped recalling per turn because it
// broke the prompt cache, and the wiki follows it: the overview is in the
// prompt, the pages behind the tools.
func wikiContext(w *wiki.Wiki, pages int, native bool) string {
	name := func(t string) string {
		if native {
			return t
		}
		return "mcp__" + KitServer + "__" + t
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<wiki pages=\"%d\">\nThis project has a knowledge wiki written from its documents (specs, designs, runbooks). "+
		"Before answering a question about the domain, the design or the documents, or changing code whose rules they describe, "+
		"search it with %s and read the pages that match with %s; name the pages you relied on. "+
		"A search result lists the pages linked to it, so follow links rather than searching again. "+
		"When you work out something worth keeping across several pages, file it with %s as a synthesis page. "+
		"When the user asks to put something into the wiki — a passage of this conversation, a finding — use %s: it is merged into the pages it belongs to.\n",
		pages, name("wiki_search"), name("wiki_read"), name("wiki_write"), name("wiki_add"))
	if o := strings.TrimSpace(w.Overview()); o != "" {
		b.WriteString("<overview>\n" + clip(o, 2000) + "\n</overview>\n")
	}
	b.WriteString("</wiki>\n\n")
	return b.String()
}

func (k *Kit) wikiTools() []agent.Extension {
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	w := k.Wiki
	return []agent.Extension{
		{
			Name: "wiki_search",
			Def: toolDef("wiki_search", "Search the project's knowledge wiki. Returns pages with a one-line summary and the pages each links to and from.",
				map[string]any{
					"query": str("What to look for, in keywords — the terms the documents would use."),
					"limit": map[string]any{"type": "integer", "description": "Most pages to return; default 10."},
					"hops":  map[string]any{"type": "integer", "description": "0-3: also return pages up to this many links from the matches, scored lower the further they are. Default 0."},
				}, "query"),
			Run: func(_ context.Context, in json.RawMessage) (string, bool) {
				var a struct {
					Query string `json:"query"`
					Limit int    `json:"limit"`
					Hops  int    `json:"hops"`
				}
				_ = json.Unmarshal(in, &a)
				res := w.Search(wiki.Query{Text: a.Query, Limit: a.Limit, Hops: min(a.Hops, 3)})
				if len(res) == 0 {
					return "nothing found; try other words, or the index: wiki_read {\"refs\": [\"index\"]}", false
				}
				var b strings.Builder
				for _, r := range res {
					fmt.Fprintf(&b, "- %s [%s] %s (score %.2f", r.Title, r.Type, r.ID, r.Score)
					if r.Hop > 0 {
						fmt.Fprintf(&b, ", %d link from %s", r.Hop, r.Via)
					}
					b.WriteString(")\n")
					if r.Snippet != "" {
						b.WriteString("  " + r.Snippet + "\n")
					}
					if len(r.Related) > 0 {
						var rel []string
						for _, n := range r.Related {
							rel = append(rel, n.Title)
						}
						b.WriteString("  linked: " + strings.Join(rel, "; ") + "\n")
					}
				}
				return b.String(), false
			},
		},
		{
			Name: "wiki_read",
			Def: toolDef("wiki_read", "Read wiki pages in full, by title or id as wiki_search shows them. \"index\" lists every page, \"overview\" is the summary, \"raw:<file>\" is an original document.",
				map[string]any{"refs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Up to 10 page titles or ids."}}, "refs"),
			Run: func(_ context.Context, in json.RawMessage) (string, bool) {
				var a struct {
					Refs []string `json:"refs"`
					Ref  string   `json:"ref"`
				}
				_ = json.Unmarshal(in, &a)
				if a.Ref != "" {
					a.Refs = append(a.Refs, a.Ref)
				}
				if len(a.Refs) == 0 {
					return "which pages? pass refs", true
				}
				if len(a.Refs) > 10 {
					a.Refs = a.Refs[:10]
				}
				g := wiki.NewGraph(w.Pages())
				var b strings.Builder
				for i, ref := range a.Refs {
					if i > 0 {
						b.WriteString("\n\n---\n\n")
					}
					switch r := strings.TrimSpace(ref); {
					case strings.EqualFold(r, "index"):
						b.WriteString(w.Index())
					case strings.EqualFold(r, "overview"):
						b.WriteString("# Overview\n\n" + w.Overview())
					case strings.HasPrefix(r, "raw:"):
						text, err := w.ReadRaw(strings.TrimPrefix(r, "raw:"))
						if err != nil {
							b.WriteString("no document " + r)
							continue
						}
						b.WriteString("# " + r + "\n\n" + clip(text, 60_000))
					default:
						id, ok := g.Resolve(r)
						if !ok {
							fmt.Fprintf(&b, "no page %q; search for it with wiki_search", r)
							continue
						}
						p := g.Pages[id]
						fmt.Fprintf(&b, "# %s\n(%s · %s · sources: %s)\n\n%s", p.Title, p.Type, p.ID, strings.Join(p.Sources, ", "), p.Body)
						if rel := g.Related(id, 15); len(rel) > 0 {
							var ts []string
							for _, n := range rel {
								ts = append(ts, n.Title)
							}
							b.WriteString("\n\nLinked pages: " + strings.Join(ts, "; "))
						}
					}
				}
				return b.String(), false
			},
		},
		{
			Name:     "wiki_write",
			Mutating: true,
			Def: toolDef("wiki_write", "File a page in the wiki: a synthesis or comparison you worked out across pages, or a correction the user gave. A page with the same type and title is replaced.",
				map[string]any{
					"type":        map[string]any{"type": "string", "enum": wiki.Types},
					"title":       str("The page's title."),
					"description": str("One sentence: what the page is about."),
					"body":        str("Markdown. Link other pages with [[Title]]."),
				}, "type", "title", "body"),
			Run: func(_ context.Context, in json.RawMessage) (string, bool) {
				var a struct {
					Type, Title, Description, Body string
				}
				_ = json.Unmarshal(in, &a)
				p := wiki.Page{Type: a.Type, Title: a.Title, Description: a.Description, Body: a.Body, Sources: []string{"agent"}}
				if old, ok := w.Get(wiki.CanonicalID(a.Type, a.Title)); ok {
					p.Sources = append(old.Sources, p.Sources...)
					p.Tags = old.Tags
				}
				p.Sources = dedupe(p.Sources)
				got, err := w.Put(p, "**agent** filed a page")
				if err != nil {
					return err.Error(), true
				}
				return "saved " + got.ID, false
			},
		},
	}
}

// wikiEmptyContext is what the prompt says of a wiki with nothing in it yet:
// only that things can be put in.
func wikiEmptyContext(native bool) string {
	name := "wiki_add"
	if !native {
		name = "mcp__" + KitServer + "__wiki_add"
	}
	return "<wiki pages=\"0\">\nThis project's knowledge wiki is empty. When the user asks to put something into the wiki — a passage of this conversation, a finding, a decision — use " + name + ".\n</wiki>\n\n"
}

// wikiAddTool puts content into the wiki the way a document goes in: kept as
// it is, then read by a model into pages — new ones, or more on the ones
// there — so a page grows from conversations as it does from files. The
// reading runs in the gateway; without one the note waits for the next
// ingest.
func (k *Kit) wikiAddTool() agent.Extension {
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	return agent.Extension{
		Name:     "wiki_add",
		Mutating: true,
		Def: toolDef("wiki_add", "Put content into the project's knowledge wiki when the user asks to (\"add this to the wiki\", \"đưa vào wiki\"): a passage of this conversation, a finding, a decision, a spec excerpt. "+
			"Pass the content whole, in its own words and language — quote what the user pointed at rather than summarising it. It is kept as a document and a model reads it into the wiki's pages, creating pages or adding to existing ones.",
			map[string]any{
				"title":   str("A short title naming what it is about, as the wiki would name the subject."),
				"content": str("The content, in Markdown, complete."),
				"message": map[string]any{"type": "integer", "description": "The index of the message it comes from, when it is one message of this conversation."},
			}, "content"),
		Run: func(ctx context.Context, in json.RawMessage) (string, bool) {
			var a struct {
				Title   string `json:"title"`
				Content string `json:"content"`
				Message *int   `json:"message"`
			}
			_ = json.Unmarshal(in, &a)
			var out struct {
				Document string `json:"document"`
				Ingest   string `json:"ingest"`
				Model    string `json:"model"`
				Error    string `json:"error"`
			}
			err := gatewayCall(ctx, "POST", "/api/wiki/add", map[string]any{
				"root": k.Root, "title": a.Title, "content": a.Content, "session": k.Session, "at": a.Message,
			}, &out)
			if err != nil {
				// No gateway: keep it, and say what reads it in.
				name, aerr := k.Wiki.AddNote(wiki.Note{Title: a.Title, Content: a.Content, From: "a conversation (" + k.Session + ")"})
				if aerr != nil {
					return aerr.Error(), true
				}
				return "kept as raw/" + name + "; the gateway is not running, so it is not in the pages yet — run `agent-tui wiki ingest`, or Ingest on the admin's Wiki page", false
			}
			switch out.Ingest {
			case "started":
				return "added as raw/" + out.Document + "; " + out.Model + " is reading it into the wiki's pages now (about a minute). Tell the user it is on the Wiki page.", false
			case "queued":
				return "added as raw/" + out.Document + "; an ingest is running and reads it as soon as it ends.", false
			case "busy":
				return "added as raw/" + out.Document + "; an ingest runs in another process, so the next ingest reads it.", false
			}
			return "added as raw/" + out.Document + ", but reading it into pages failed: " + out.Error, false
		},
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
