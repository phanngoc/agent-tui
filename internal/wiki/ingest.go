package wiki

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// LLM is what an ingest needs from a model: a system prompt and a user
// message in, text out. learn.LLM is one.
type LLM interface {
	Complete(ctx context.Context, system, user string, maxTokens int64) (string, error)
}

// Ingest limits, MemoryKnowledge's unless noted.
const (
	chunkBudget     = 28_000 // characters of a document shown in one call
	chunkOverlap    = 400
	concurrency     = 3 // documents read at once
	appendOver      = 4000
	existingListed  = 300 // existing pages listed to the model; the most relevant when there are more
	analysisTokens  = 4000
	generateTokens  = 16000
	mergeTokens     = 8000
	overviewTokens  = 2000
	overviewMinimum = 2
	staleLock       = 2 * time.Hour
)

// Report is what an ingest did.
type Report struct {
	Ingested []string          `json:"ingested"`
	Skipped  int               `json:"skipped"`
	Deleted  []string          `json:"deleted"`
	Failed   map[string]string `json:"failed"`
	Written  []string          `json:"written"`
	Removed  []string          `json:"removed"`
	Took     time.Duration     `json:"took"`
}

// ErrBusy is returned while another ingest of the same wiki runs.
var ErrBusy = errors.New("this wiki is already being ingested")

// candidate is a page a model wrote from one source.
type candidate struct {
	page   Page
	source string
}

// Ingest reads every raw file that is new or changed since the last ingest
// into pages, and takes out what came only from files that are gone. A file
// that did not change is not read again. progress, when set, hears what is
// happening.
func (w *Wiki) Ingest(ctx context.Context, llm LLM, progress func(string)) (Report, error) {
	rep := Report{Failed: map[string]string{}}
	start := time.Now()
	say := func(f string, a ...any) {
		if progress != nil {
			progress(fmt.Sprintf(f, a...))
		}
	}
	if err := w.Init(); err != nil {
		return rep, err
	}
	unlock, err := w.lock()
	if err != nil {
		return rep, err
	}
	defer unlock()

	st := w.State()
	raw := w.Raw()
	present := map[string]RawFile{}
	var todo []RawFile
	for _, r := range raw {
		present[r.Name] = r
		s, ok := st.Sources[r.Name]
		if ok && s.Status == "ingested" && s.SHA256 == r.SHA256 {
			rep.Skipped++
			continue
		}
		todo = append(todo, r)
	}
	for name := range st.Sources {
		if _, ok := present[name]; !ok {
			rep.Deleted = append(rep.Deleted, name)
		}
	}
	sort.Strings(rep.Deleted)
	say("%d to read, %d unchanged, %d removed", len(todo), rep.Skipped, len(rep.Deleted))

	// What came only from a removed file goes; what came from a changed file
	// is written again from its new text rather than merged with the old.
	w.mu.Lock()
	gone := map[string]bool{}
	for _, name := range rep.Deleted {
		gone[name] = true
	}
	changed := map[string]bool{}
	for _, r := range todo {
		if s, ok := st.Sources[r.Name]; ok && s.SHA256 != r.SHA256 {
			changed[r.Name] = true
		}
	}
	rep.Removed = w.cascade(gone, changed)
	w.mu.Unlock()
	for name := range gone {
		delete(st.Sources, name)
	}

	// Read the documents, a few at a time. Each sees the wiki as it was
	// before any of them; pages two of them write are merged at commit.
	snapshot := w.Pages()
	var (
		mu    sync.Mutex
		cands []candidate
		wg    sync.WaitGroup
		sem   = make(chan struct{}, concurrency)
	)
	for _, r := range todo {
		wg.Add(1)
		go func(r RawFile) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			say("reading %s", r.Name)
			got, err := w.extract(ctx, llm, r.Name, snapshot)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				rep.Failed[r.Name] = err.Error()
				st.Sources[r.Name] = &Source{SHA256: r.SHA256, Status: "failed", Error: err.Error(), Ingested: time.Now().UTC()}
				say("%s failed: %v", r.Name, err)
				return
			}
			cands = append(cands, got...)
			say("%s gave %d pages", r.Name, len(got))
		}(r)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return rep, err
	}

	// Commit, one page at a time, merging into what is there.
	// In a fixed order, whatever order the documents finished in.
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].page.ID != cands[j].page.ID {
			return cands[i].page.ID < cands[j].page.ID
		}
		return cands[i].source < cands[j].source
	})
	bySource := map[string][]string{}
	for _, c := range cands {
		say("writing %s", c.page.ID)
		p, err := w.commit(ctx, llm, c)
		if err != nil {
			rep.Failed[c.source+" → "+c.page.ID] = err.Error()
			continue
		}
		rep.Written = union(rep.Written, []string{p.ID})
		bySource[c.source] = union(bySource[c.source], []string{p.ID})
	}
	for _, r := range todo {
		if _, failed := rep.Failed[r.Name]; failed {
			continue
		}
		rep.Ingested = append(rep.Ingested, r.Name)
		st.Sources[r.Name] = &Source{SHA256: r.SHA256, Status: "ingested", Pages: bySource[r.Name], Ingested: time.Now().UTC()}
	}

	if len(rep.Written) > 0 || len(rep.Removed) > 0 {
		if pages := w.Pages(); len(pages) >= overviewMinimum {
			say("writing the overview")
			if err := w.writeOverview(ctx, llm, pages); err != nil {
				say("overview failed: %v", err)
			}
		}
	}
	w.mu.Lock()
	w.rebuildIndex()
	what := fmt.Sprintf("**ingest** %d documents read, %d pages written", len(rep.Ingested), len(rep.Written))
	if len(rep.Removed) > 0 {
		what += fmt.Sprintf(", %d removed", len(rep.Removed))
	}
	if len(rep.Failed) > 0 {
		what += fmt.Sprintf(", %d failed", len(rep.Failed))
	}
	if len(todo) > 0 || len(rep.Deleted) > 0 {
		w.appendLog(what, rep.Written)
	}
	w.mu.Unlock()

	st.Version++
	st.Ingested = time.Now().UTC()
	rep.Took = time.Since(start).Round(time.Second)
	if err := w.saveState(st); err != nil {
		return rep, err
	}
	if len(todo) > 0 && len(rep.Ingested) == 0 {
		return rep, errors.New("every document failed; see the report")
	}
	return rep, nil
}

// lock keeps two ingests of one wiki apart, in this process and across the
// terminal and the gateway.
func (w *Wiki) lock() (func(), error) {
	file := filepath.Join(w.Dir, "ingest.lock")
	if st, err := os.Stat(file); err == nil && time.Since(st.ModTime()) > staleLock {
		_ = os.Remove(file)
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil, ErrBusy
		}
		return nil, err
	}
	fmt.Fprintf(f, "%d %s\n", os.Getpid(), time.Now().Format(time.RFC3339))
	f.Close()
	return func() { _ = os.Remove(file) }, nil
}

// Busy reports whether an ingest is running.
func (w *Wiki) Busy() bool {
	st, err := os.Stat(filepath.Join(w.Dir, "ingest.lock"))
	return err == nil && time.Since(st.ModTime()) < staleLock
}

// cascade takes out what the gone files leave behind and the pages a changed
// file wrote alone, and returns the ids removed. Pages written by hand stay;
// links to removed pages become plain text. Called with w.mu held.
func (w *Wiki) cascade(gone, changed map[string]bool) []string {
	if len(gone) == 0 && len(changed) == 0 {
		return nil
	}
	pages := w.Pages()
	before := NewGraph(pages)
	var removed []string
	removedSet := map[string]bool{}
	for _, p := range pages {
		if p.Locked {
			continue
		}
		kept := p.Sources[:0:0]
		hit := false
		for _, s := range p.Sources {
			if gone[s] {
				hit = true
				continue
			}
			kept = append(kept, s)
		}
		sole := len(p.Sources) == 1 && changed[p.Sources[0]]
		switch {
		case (hit && len(kept) == 0) || sole:
			if w.remove(p.ID) == nil {
				removed = append(removed, p.ID)
				removedSet[p.ID] = true
			}
		case hit:
			p.Sources = kept
			_, _ = w.put(p)
		}
	}
	if len(removed) == 0 {
		return nil
	}
	for _, p := range w.Pages() {
		body := linkRE.ReplaceAllStringFunc(p.Body, func(m string) string {
			sub := linkRE.FindStringSubmatch(m)
			if id, ok := before.Resolve(sub[1]); ok && removedSet[id] {
				if strings.TrimSpace(sub[2]) != "" {
					return sub[2]
				}
				return sub[1]
			}
			return m
		})
		if body != p.Body {
			p.Body = body
			_, _ = w.put(p)
		}
	}
	w.appendLog("**removed** pages of deleted or changed documents", removed)
	return removed
}

// extract reads one document into candidate pages: for each chunk, a plan
// first, then the pages written from it.
func (w *Wiki) extract(ctx context.Context, llm LLM, name string, snapshot []Page) ([]candidate, error) {
	text, err := w.ReadRaw(name)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("the document is empty")
	}
	chunks := Chunk(text, chunkBudget, chunkOverlap)
	fill := func(tpl string, kv ...string) string {
		r := strings.NewReplacer(kv...)
		return r.Replace(tpl)
	}
	sys := func(tpl string) string {
		return fill(tpl, "{{purpose}}", w.Purpose(), "{{schema}}", w.Schema())
	}
	var out []candidate
	for i, c := range chunks {
		part := ""
		if len(chunks) > 1 {
			part = fmt.Sprintf(" (part %d of %d)", i+1, len(chunks))
		}
		existing := existingList(snapshot, c)
		plan, err := llm.Complete(ctx, sys(analysisSystem),
			fill(analysisUser, "{{existing}}", existing, "{{source}}", name, "{{part}}", part, "{{text}}", c), analysisTokens)
		if err != nil {
			return nil, fmt.Errorf("analysis: %w", err)
		}
		plan = strings.TrimSpace(thinkRE.ReplaceAllString(plan, ""))
		if plan == "" {
			plan = "(no plan: extract the entities and concepts yourself)"
		}
		gen, err := llm.Complete(ctx, sys(generateSystem),
			fill(generateUser, "{{plan}}", plan, "{{existing}}", existing, "{{source}}", name, "{{part}}", part, "{{text}}", c), generateTokens)
		if err != nil {
			return nil, fmt.Errorf("generation: %w", err)
		}
		blocks := ParseFileBlocks(gen)
		if len(blocks) == 0 {
			w.debug("generate", name, gen)
			return nil, errors.New("the model wrote no pages")
		}
		for _, b := range blocks {
			p := ParsePage("", b.Body)
			if strings.TrimSpace(p.Title) == "" || strings.TrimSpace(p.Body) == "" {
				continue
			}
			if p.Type == "" {
				p.Type = typeOfDir(path.Base(path.Dir(b.Path)))
			}
			p.ID = CanonicalID(p.Type, p.Title)
			p.Sources = []string{name}
			p.Locked = false
			p.Updated = ""
			out = append(out, candidate{page: p, source: name})
		}
	}
	if len(out) == 0 {
		return nil, errors.New("the model's pages had no title or body")
	}
	return out, nil
}

// existingList is the pages a model is told exist, as "- [type] Title — description".
// A large wiki lists the pages nearest the text.
func existingList(pages []Page, text string) string {
	if len(pages) == 0 {
		return "(none yet)"
	}
	shown := pages
	if len(pages) > existingListed {
		g := NewGraph(pages)
		shown = nil
		for _, r := range g.bm25(text) {
			shown = append(shown, g.Pages[r.ID])
			if len(shown) == existingListed {
				break
			}
		}
	}
	var b strings.Builder
	for _, p := range shown {
		fmt.Fprintf(&b, "- [%s] %s", p.Type, p.Title)
		if p.Description != "" {
			b.WriteString(" — " + p.Description)
		}
		b.WriteString("\n")
	}
	if len(shown) < len(pages) {
		fmt.Fprintf(&b, "(and %d more, less related)\n", len(pages)-len(shown))
	}
	return b.String()
}

// commit writes one candidate, merged into the page already there.
func (w *Wiki) commit(ctx context.Context, llm LLM, c candidate) (Page, error) {
	w.mu.Lock()
	old, exists := w.Get(c.page.ID)
	w.mu.Unlock()
	p := c.page
	if exists {
		merged, err := w.merge(ctx, llm, old, c.page)
		if err != nil {
			return old, err
		}
		p = merged
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.put(p)
}

// merge folds a new page into an old one: a hand-edited page is left alone, a
// page that already says it all only gains the source, a long page has what
// is new appended, and a short one is rewritten with both.
func (w *Wiki) merge(ctx context.Context, llm LLM, old, cand Page) (Page, error) {
	if old.Locked {
		return old, nil
	}
	out := old
	out.Sources = union(append([]string(nil), old.Sources...), cand.Sources)
	out.Tags = union(append([]string(nil), old.Tags...), cand.Tags)
	if out.Description == "" {
		out.Description = cand.Description
	}
	if strings.Contains(squash(old.Body), squash(cand.Body)) {
		return out, nil
	}
	if len(old.Body) > appendOver {
		add, err := llm.Complete(ctx, appendSystem,
			"## Existing page\n"+old.String()+"\n\n## New material\n"+cand.String(), mergeTokens)
		if err != nil {
			return old, err
		}
		if add = strings.TrimSpace(thinkRE.ReplaceAllString(add, "")); add != "" {
			out.Body = strings.TrimSpace(old.Body) + "\n\n" + add
		}
		return out, nil
	}
	text, err := llm.Complete(ctx, mergeSystem, "## Old page\n"+old.String()+"\n\n## New page\n"+cand.String(), mergeTokens)
	if err != nil {
		return old, err
	}
	text = strings.TrimSpace(thinkRE.ReplaceAllString(text, ""))
	if m := fenceRE.FindStringSubmatch(text); m != nil && strings.HasPrefix(strings.TrimSpace(m[1]), "---") {
		text = strings.TrimSpace(m[1])
	}
	merged := ParsePage(old.ID, text)
	if !strings.HasPrefix(text, "---") || strings.TrimSpace(merged.Body) == "" {
		// Not a page: keep the old one whole and add the new beneath it.
		out.Body = strings.TrimSpace(old.Body) + "\n\n" + strings.TrimSpace(cand.Body)
		return out, nil
	}
	out.Body = merged.Body
	if merged.Description != "" {
		out.Description = merged.Description
	}
	out.Tags = union(out.Tags, merged.Tags)
	return out, nil
}

func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

// writeOverview has a model write overview.md from the pages' titles and
// descriptions.
func (w *Wiki) writeOverview(ctx context.Context, llm LLM, pages []Page) error {
	var b strings.Builder
	for _, p := range pages {
		fmt.Fprintf(&b, "- [%s] %s", p.Type, p.Title)
		if p.Description != "" {
			b.WriteString(" — " + p.Description)
		}
		b.WriteString("\n")
		if b.Len() > 60_000 {
			b.WriteString("…\n")
			break
		}
	}
	text, err := llm.Complete(ctx, overviewSystem, "## Purpose\n"+w.Purpose()+"\n\n## Pages\n"+b.String(), overviewTokens)
	if err != nil {
		return err
	}
	text = strings.TrimSpace(thinkRE.ReplaceAllString(text, ""))
	if text == "" {
		return errors.New("empty overview")
	}
	p := Page{Type: TypeSynthesis, Title: "Overview", Description: "What this wiki covers and where to start.",
		Updated: time.Now().Format("2006-01-02"), Body: text}
	return writeAtomic(filepath.Join(w.Dir, "overview.md"), []byte(p.String()))
}

// debug keeps an answer that could not be read, for whoever wonders why.
func (w *Wiki) debug(stage, source, text string) {
	dir := filepath.Join(w.Dir, "_debug")
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	name := fmt.Sprintf("%s-%s-%s.txt", stage, Slugify(source), time.Now().Format("20060102-150405"))
	_ = os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644)
}

var (
	thinkRE = regexp.MustCompile(`(?s)<think>.*?</think>`)
	fenceRE = regexp.MustCompile("(?s)```(?:markdown|md|yaml)?\\s*\n(.*?)```")
	// A FILE block opens with <<<FILE path="…">>> (or >>); one never closed
	// was cut off and is dropped.
	fileOpenRE = regexp.MustCompile(`<<<FILE\s+path\s*=\s*"([^"\n]+)"\s*>>>?`)
)

// FileBlock is one page in a model's answer.
type FileBlock struct {
	Path string
	Body string
}

// ParseFileBlocks reads the FILE blocks of an answer. Paths outside pages/
// are dropped, and so are blocks never closed.
func ParseFileBlocks(text string) []FileBlock {
	var out []FileBlock
	for {
		loc := fileOpenRE.FindStringSubmatchIndex(text)
		if loc == nil {
			return out
		}
		p := text[loc[2]:loc[3]]
		rest := text[loc[1]:]
		end := strings.Index(rest, "<<<END>>>")
		next := fileOpenRE.FindStringIndex(rest)
		if end < 0 || (next != nil && next[0] < end) {
			if next == nil {
				return out
			}
			text = rest[next[0]:]
			continue
		}
		body := strings.Trim(rest[:end], "\r\n")
		if clean, ok := cleanBlockPath(p); ok {
			out = append(out, FileBlock{Path: clean, Body: body})
		}
		text = rest[end+len("<<<END>>>"):]
	}
}

func cleanBlockPath(p string) (string, bool) {
	p = strings.TrimSpace(strings.ReplaceAll(p, `\`, "/"))
	if strings.Contains(p, "..") || strings.HasPrefix(p, "/") || strings.Contains(p, ":") {
		return "", false
	}
	p = strings.TrimPrefix(p, "./")
	if strings.HasPrefix(p, "wiki/") {
		p = "pages/" + strings.TrimPrefix(p, "wiki/")
	}
	if !strings.HasPrefix(p, "pages/") {
		p = "pages/" + p
	}
	return path.Clean(p), true
}

var headingRE = regexp.MustCompile(`(?m)^#{1,6}\s`)

// Chunk splits a long document where its headings are, then at blank lines,
// then anywhere, packing the pieces into chunks of about budget characters
// that each begin with the last overlap characters of the one before.
func Chunk(text string, budget, overlap int) []string {
	if len(text) <= budget {
		return []string{text}
	}
	var units []string
	idx := headingRE.FindAllStringIndex(text, -1)
	prev := 0
	for _, ix := range idx {
		if ix[0] > prev {
			units = append(units, text[prev:ix[0]])
		}
		prev = ix[0]
	}
	units = append(units, text[prev:])
	var small []string
	for _, u := range units {
		if len(u) <= budget {
			small = append(small, u)
			continue
		}
		for _, para := range strings.SplitAfter(u, "\n\n") {
			for len(para) > budget {
				cut := budget
				for cut > budget/2 && para[cut-1] != '\n' && para[cut-1] != ' ' {
					cut--
				}
				for cut > 1 && !utf8.RuneStart(para[cut]) {
					cut--
				}
				small = append(small, para[:cut])
				para = para[cut:]
			}
			if para != "" {
				small = append(small, para)
			}
		}
	}
	var out []string
	var cur strings.Builder
	for _, u := range small {
		if cur.Len() > 0 && cur.Len()+len(u) > budget {
			done := cur.String()
			out = append(out, done)
			cur.Reset()
			if overlap > 0 && len(done) > overlap {
				from := len(done) - overlap
				for from < len(done) && !utf8.RuneStart(done[from]) {
					from++
				}
				tail := done[from:]
				if i := strings.IndexAny(tail, "\n "); i >= 0 {
					tail = tail[i+1:]
				}
				cur.WriteString(tail)
			}
		}
		cur.WriteString(u)
	}
	if strings.TrimSpace(cur.String()) != "" {
		out = append(out, cur.String())
	}
	return out
}
