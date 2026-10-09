package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/phanngoc/agent-tui/internal/config"
	"github.com/phanngoc/agent-tui/internal/gateway"
	"github.com/phanngoc/agent-tui/internal/learn"
	"github.com/phanngoc/agent-tui/internal/wiki"
)

// wikiJob is the last ingest the gateway ran for one wiki: it runs on after
// the request that started it, and the page polls for how it is going.
type wikiJob struct {
	Running  bool         `json:"running"`
	Started  time.Time    `json:"started"`
	Model    string       `json:"model,omitempty"`
	Progress []string     `json:"progress"`
	Report   *wiki.Report `json:"report,omitempty"`
	Error    string       `json:"error,omitempty"`
	// Again asks for another run when this one ends: something was added
	// while it ran.
	Again bool `json:"again,omitempty"`
}

var (
	wikiJobsMu sync.Mutex
	wikiJobs   = map[string]*wikiJob{} // wiki dir -> its last ingest
)

func wikiJobOf(dir string) wikiJob {
	wikiJobsMu.Lock()
	defer wikiJobsMu.Unlock()
	if j, ok := wikiJobs[dir]; ok {
		cp := *j
		cp.Progress = append([]string(nil), j.Progress...)
		return cp
	}
	return wikiJob{Progress: []string{}}
}

// wikiPage is a page as the admin lists it.
type wikiPage struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Sources     []string `json:"sources"`
	Tags        []string `json:"tags"`
	Updated     string   `json:"updated"`
	Locked      bool     `json:"locked"`
	Links       int      `json:"links"`
	Backlinks   int      `json:"backlinks"`
}

func (s *Server) wikiRoutes(m *http.ServeMux) {
	// GET /api/wiki?root= is the wiki at a glance: numbers, pages, documents
	// and how each fared, the last ingest, and what lint found.
	m.HandleFunc("GET /api/wiki", func(w http.ResponseWriter, r *http.Request) {
		wk := wiki.For(r.URL.Query().Get("root"))
		pages := wk.Pages()
		g := wiki.NewGraph(pages)
		list := []wikiPage{}
		for _, p := range pages {
			list = append(list, wikiPage{ID: p.ID, Type: p.Type, Title: p.Title, Description: p.Description,
				Sources: p.Sources, Tags: p.Tags, Updated: p.Updated, Locked: p.Locked,
				Links: len(g.Out[p.ID]), Backlinks: len(g.In[p.ID])})
		}
		type doc struct {
			wiki.RawFile
			Status   string    `json:"status"` // ingested, failed, changed or new
			Error    string    `json:"error,omitempty"`
			Pages    []string  `json:"pages"`
			Ingested time.Time `json:"ingested,omitzero"`
		}
		st := wk.State()
		docs := []doc{}
		for _, f := range wk.Raw() {
			d := doc{RawFile: f, Status: "new", Pages: []string{}}
			if src, ok := st.Sources[f.Name]; ok {
				d.Status, d.Error, d.Ingested = src.Status, src.Error, src.Ingested
				if src.Pages != nil {
					d.Pages = src.Pages
				}
				if src.SHA256 != f.SHA256 {
					d.Status = "changed"
				}
			}
			docs = append(docs, d)
		}
		writeJSON(w, map[string]any{"stats": wk.Stats(), "pages": list, "documents": docs,
			"overview": wk.Overview(), "lint": wk.Lint(), "job": wikiJobOf(wk.Dir), "busy": wk.Busy(),
			"purpose": wk.Purpose(), "schema": wk.Schema(), "types": wiki.Types})
	})

	// GET /api/wiki/page?root=&id= is one page, with its neighbours and the
	// earlier versions kept of it.
	m.HandleFunc("GET /api/wiki/page", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		wk := wiki.For(q.Get("root"))
		g := wiki.NewGraph(wk.Pages())
		id, ok := g.Resolve(q.Get("id"))
		if !ok {
			fail(w, http.StatusNotFound, errors.New("no page "+q.Get("id")))
			return
		}
		p := g.Pages[id]
		writeJSON(w, map[string]any{"page": wikiPage{ID: p.ID, Type: p.Type, Title: p.Title, Description: p.Description,
			Sources: p.Sources, Tags: p.Tags, Updated: p.Updated, Locked: p.Locked,
			Links: len(g.Out[id]), Backlinks: len(g.In[id])},
			"body": p.Body, "related": g.Related(id, 0), "broken": g.Broken[id], "history": len(wk.History(id))})
	})

	// PUT /api/wiki/page saves a page edited by hand. It is locked: an ingest
	// will not merge into it again.
	m.HandleFunc("PUT /api/wiki/page", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Root        string `json:"root"`
			ID          string `json:"id"`
			Type        string `json:"type"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Body        string `json:"body"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		wk := wiki.For(in.Root)
		p := wiki.Page{Type: in.Type, Title: in.Title, Description: in.Description, Body: in.Body, Locked: true, Sources: []string{"user"}}
		if old, ok := wk.Get(in.ID); ok {
			p.Sources, p.Tags, p.Extra = old.Sources, old.Tags, old.Extra
			p.Sources = append(p.Sources[:len(p.Sources):len(p.Sources)], "user")
			if p.Type == old.Type && p.Title == old.Title {
				p.ID = old.ID
			} else if err := wk.Remove(old.ID, ""); err != nil {
				fail(w, http.StatusInternalServerError, err)
				return
			}
		}
		got, err := wk.Put(p, "**edit** by hand")
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, map[string]any{"id": got.ID})
	})

	m.HandleFunc("DELETE /api/wiki/page", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if err := wiki.For(q.Get("root")).Remove(q.Get("id"), "**delete** by hand"); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})

	// GET /api/wiki/search?root=&q=&hops= searches as the agent does.
	m.HandleFunc("GET /api/wiki/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		hops, _ := strconv.Atoi(q.Get("hops"))
		res := wiki.For(q.Get("root")).Search(wiki.Query{Text: q.Get("q"), Hops: hops, Limit: 30})
		if res == nil {
			res = []wiki.Result{}
		}
		writeJSON(w, res)
	})

	// GET /api/wiki/graph?root= is every page and link, for a picture.
	m.HandleFunc("GET /api/wiki/graph", func(w http.ResponseWriter, r *http.Request) {
		g := wiki.NewGraph(wiki.For(r.URL.Query().Get("root")).Pages())
		type node struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Type  string `json:"type"`
		}
		nodes, edges := []node{}, [][2]string{}
		for id, p := range g.Pages {
			nodes = append(nodes, node{ID: id, Title: p.Title, Type: p.Type})
			for _, to := range g.Out[id] {
				edges = append(edges, [2]string{id, to})
			}
		}
		writeJSON(w, map[string]any{"nodes": nodes, "edges": edges})
	})

	// POST /api/wiki/raw keeps documents: {root, files: [{name, content}]}.
	m.HandleFunc("POST /api/wiki/raw", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Root  string `json:"root"`
			Files []struct {
				Name    string `json:"name"`
				Content string `json:"content"`
			} `json:"files"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		wk := wiki.For(in.Root)
		added, failed := []string{}, map[string]string{}
		for _, f := range in.Files {
			name, err := wk.AddRaw(f.Name, []byte(f.Content))
			if err != nil {
				failed[f.Name] = err.Error()
				continue
			}
			added = append(added, name)
		}
		writeJSON(w, map[string]any{"added": added, "failed": failed, "pending": len(wk.Pending())})
	})

	m.HandleFunc("GET /api/wiki/raw", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		text, err := wiki.For(q.Get("root")).ReadRaw(q.Get("name"))
		if err != nil {
			fail(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, map[string]any{"name": q.Get("name"), "content": text})
	})

	m.HandleFunc("DELETE /api/wiki/raw", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if err := wiki.For(q.Get("root")).RemoveRaw(q.Get("name")); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})

	// PUT /api/wiki/steer saves purpose.md and schema.md, which every
	// ingest reads.
	m.HandleFunc("PUT /api/wiki/steer", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Root    string  `json:"root"`
			Purpose *string `json:"purpose"`
			Schema  *string `json:"schema"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if err := wiki.For(in.Root).SetSteering(in.Purpose, in.Schema); err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})

	// POST /api/wiki/ingest starts an ingest and returns at once; GET
	// /api/wiki shows how it goes.
	m.HandleFunc("POST /api/wiki/ingest", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Root  string `json:"root"`
			Model string `json:"model"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		wk := wiki.For(in.Root)
		state, model, err := startWikiIngest(wk, in.Model, false)
		switch {
		case err != nil:
			fail(w, http.StatusBadRequest, err)
		case state != "started":
			fail(w, http.StatusConflict, wiki.ErrBusy)
		default:
			writeJSON(w, map[string]any{"started": true, "model": model})
		}
	})

	// POST /api/wiki/add puts a note into the wiki — a passage of a
	// conversation, something worked out — as a document, and reads it into
	// pages at once: new pages, or more on the ones there. An ingest already
	// running here reads it next.
	m.HandleFunc("POST /api/wiki/add", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Root    string `json:"root"`
			Title   string `json:"title"`
			Content string `json:"content"`
			// Session and At say where in which conversation it came from.
			Session string `json:"session"`
			At      *int   `json:"at"`
			// NoIngest keeps the document without reading it yet.
			NoIngest bool `json:"no_ingest"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		from := ""
		if in.Session != "" {
			if sess, err := gateway.Load(in.Session); err == nil {
				from = fmt.Sprintf("the conversation «%s» (%s)", sess.Label(), sess.ID)
				if in.Root == "" {
					in.Root = sess.Root
				}
			} else {
				from = "conversation " + in.Session
			}
			if in.At != nil {
				from += fmt.Sprintf(", message %d", *in.At+1)
			}
		}
		wk := wiki.For(in.Root)
		name, err := wk.AddNote(wiki.Note{Title: in.Title, Content: in.Content, From: from})
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		out := map[string]any{"document": name, "ingest": "not started"}
		if !in.NoIngest {
			state, model, err := startWikiIngest(wk, "", true)
			if err != nil {
				out["ingest"], out["error"] = "failed", err.Error()
			} else {
				out["ingest"], out["model"] = state, model
			}
		}
		writeJSON(w, out)
	})
}

// startWikiIngest starts reading a wiki's new documents in the background and
// says how it went: "started"; "queued" when one runs here already and queue
// was asked for, so it runs again when that one ends; "busy" when one runs in
// another process (a shell's `agent-tui wiki ingest`), which leaves the
// documents for the next.
func startWikiIngest(wk *wiki.Wiki, model string, queue bool) (state, modelName string, err error) {
	wikiJobsMu.Lock()
	defer wikiJobsMu.Unlock()
	if j, ok := wikiJobs[wk.Dir]; ok && j.Running {
		if queue {
			j.Again = true
			return "queued", j.Model, nil
		}
		return "busy", j.Model, nil
	}
	if wk.Busy() {
		return "busy", "", nil
	}
	if model == "" {
		model = config.LoadPrefs().LearnModel
	}
	llm, err := learn.NewLLM(model)
	if err != nil {
		return "", "", err
	}
	job := &wikiJob{Running: true, Started: time.Now().UTC(), Model: llm.Name(), Progress: []string{}}
	wikiJobs[wk.Dir] = job
	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
			rep, err := wk.Ingest(ctx, llm, func(line string) {
				wikiJobsMu.Lock()
				defer wikiJobsMu.Unlock()
				job.Progress = append(job.Progress, time.Now().Format("15:04:05")+"  "+line)
				if len(job.Progress) > 300 {
					job.Progress = job.Progress[len(job.Progress)-300:]
				}
			})
			cancel()
			wikiJobsMu.Lock()
			job.Report, job.Error = &rep, ""
			if err != nil {
				job.Error = err.Error()
			}
			// Something was added while this ran: read it now, not at the
			// next ingest someone remembers to start.
			if job.Again {
				job.Again = false
				job.Progress = append(job.Progress, time.Now().Format("15:04:05")+"  again, for what was added meanwhile")
				wikiJobsMu.Unlock()
				continue
			}
			job.Running = false
			wikiJobsMu.Unlock()
			return
		}
	}()
	return "started", job.Model, nil
}
