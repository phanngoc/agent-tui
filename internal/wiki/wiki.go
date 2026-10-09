// Package wiki is a project's knowledge base: documents read once by a model
// and kept as a wiki of linked Markdown pages, in the shape of the LLM wiki
// Andrej Karpathy described and TencentDB Agent Memory's MemoryKnowledge
// built.
//
// The files are the wiki. Raw documents go in raw/ and are never changed; the
// pages a model writes from them go in pages/<type>/<slug>.md, each with a
// header naming the raw files it came from; index.md and log.md are rebuilt
// without a model after every change, and overview.md is the one page a model
// writes about the whole. Search, links and backlinks are worked out from the
// files whenever they are asked for, so a page edited by hand counts at once
// and there is no index to fall out of step.
//
// Memory (package memory) is what was said in conversations; the wiki is
// what the documents say. An agent reaches both through tools, and the wiki
// only enters a prompt when a page is asked for.
package wiki

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/phanngoc/agent-tui/internal/config"
)

// Limits on what goes in.
const (
	MaxRawBytes  = 2 << 20 // one raw document
	MaxPageBytes = 512 << 10
)

// Wiki is one wiki on disk:
//
//	raw/            the documents, as they were given
//	pages/<type>/   the pages, one Markdown file each
//	index.md        every page by type, rebuilt after each change
//	log.md          what each ingest and edit did, newest first
//	overview.md     a model's account of the whole
//	purpose.md      what this wiki is for — the user's to edit, read by ingest
//	schema.md       what to extract and how — the same
//	history/        each page as it was before it was overwritten
//	state.json      every raw file's hash and whether it was ingested
//	_debug/         answers a model gave that could not be read
type Wiki struct {
	Dir string

	mu sync.Mutex
}

var wikis sync.Map // dir -> *Wiki, so one lock per directory

// Open returns the wiki in dir, creating nothing until the first write.
func Open(dir string) *Wiki {
	v, _ := wikis.LoadOrStore(filepath.Clean(dir), &Wiki{Dir: filepath.Clean(dir)})
	return v.(*Wiki)
}

// DirFor is where a project's wiki lives: beside its memory, in the data
// directory. Without a project it is the user's own.
func DirFor(root string) string {
	if root == "" {
		return filepath.Join(config.DataDir(), "wiki")
	}
	return filepath.Join(config.ProjectDataDir(root), "wiki")
}

// For returns a project's wiki.
func For(root string) *Wiki { return Open(DirFor(root)) }

func (w *Wiki) pagesDir() string { return filepath.Join(w.Dir, "pages") }

// RawDir is where the documents are kept.
func (w *Wiki) RawDir() string { return filepath.Join(w.Dir, "raw") }

// Init lays the wiki out, writing the purpose and schema it starts from.
func (w *Wiki) Init() error {
	for _, d := range []string{w.RawDir(), w.pagesDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	for name, body := range map[string]string{"purpose.md": defaultPurpose, "schema.md": defaultSchema} {
		p := filepath.Join(w.Dir, name)
		if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

// Exists reports whether anything was ever put in the wiki.
func (w *Wiki) Exists() bool {
	_, err := os.Stat(w.Dir)
	return err == nil
}

func (w *Wiki) readFile(name string) string {
	b, err := os.ReadFile(filepath.Join(w.Dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Purpose is purpose.md, or the default when there is none.
func (w *Wiki) Purpose() string {
	if s := w.readFile("purpose.md"); s != "" {
		return s
	}
	return defaultPurpose
}

// Schema is schema.md, or the default.
func (w *Wiki) Schema() string {
	if s := w.readFile("schema.md"); s != "" {
		return s
	}
	return defaultSchema
}

// SetSteering saves purpose.md and schema.md; a nil one is left as it is.
func (w *Wiki) SetSteering(purpose, schema *string) error {
	if err := w.Init(); err != nil {
		return err
	}
	for name, v := range map[string]*string{"purpose.md": purpose, "schema.md": schema} {
		if v == nil {
			continue
		}
		if err := writeAtomic(filepath.Join(w.Dir, name), []byte(strings.TrimSpace(*v)+"\n")); err != nil {
			return err
		}
	}
	return nil
}

// Overview is overview.md without its header.
func (w *Wiki) Overview() string { return ParsePage("overview", w.readFile("overview.md")).Body }

// Index is index.md.
func (w *Wiki) Index() string { return w.readFile("index.md") }

// Log is log.md.
func (w *Wiki) Log() string { return w.readFile("log.md") }

// Count is how many pages there are, without reading them.
func (w *Wiki) Count() int {
	files, _ := filepath.Glob(filepath.Join(w.pagesDir(), "*", "*.md"))
	return len(files)
}

// Pages reads every page, by id.
func (w *Wiki) Pages() []Page {
	var out []Page
	root := w.pagesDir()
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		id := strings.TrimSuffix(filepath.ToSlash(rel), ".md")
		pg := ParsePage(id, string(b))
		if pg.Title == "" {
			pg.Title = path.Base(id)
		}
		if pg.Type == "" {
			pg.Type = typeOfDir(path.Dir(id))
		}
		out = append(out, pg)
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func validID(id string) bool {
	if id == "" || strings.Contains(id, "..") || strings.ContainsAny(id, `\:`) || strings.HasPrefix(id, "/") {
		return false
	}
	return path.Clean(id) == id && strings.Count(id, "/") == 1
}

func (w *Wiki) pageFile(id string) string {
	return filepath.Join(w.pagesDir(), filepath.FromSlash(id)+".md")
}

// Get reads one page by id.
func (w *Wiki) Get(id string) (Page, bool) {
	id = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(id, "pages/"), "/"), ".md")
	if !validID(id) {
		return Page{}, false
	}
	b, err := os.ReadFile(w.pageFile(id))
	if err != nil {
		return Page{}, false
	}
	return ParsePage(id, string(b)), true
}

// Put writes a page where its type and title say it belongs, keeping the one
// it replaces in history/. why goes in the log.
func (w *Wiki) Put(p Page, why string) (Page, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	p, err := w.put(p)
	if err != nil {
		return p, err
	}
	if why != "" {
		w.appendLog(why, []string{p.ID})
	}
	w.rebuildIndex()
	return p, nil
}

func (w *Wiki) put(p Page) (Page, error) {
	if strings.TrimSpace(p.Title) == "" {
		return p, errors.New("a page needs a title")
	}
	if strings.TrimSpace(p.Body) == "" {
		return p, errors.New("a page needs a body")
	}
	if p.Type == "" {
		p.Type = TypeConcept
	}
	if p.ID == "" {
		p.ID = CanonicalID(p.Type, p.Title)
	}
	if !validID(p.ID) {
		return p, fmt.Errorf("not a page id: %q", p.ID)
	}
	p.Updated = time.Now().Format("2006-01-02")
	text := p.String()
	if len(text) > MaxPageBytes {
		return p, fmt.Errorf("page %s is %d bytes, over the %d limit", p.ID, len(text), MaxPageBytes)
	}
	file := w.pageFile(p.ID)
	w.snapshot(p.ID)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return p, err
	}
	return p, writeAtomic(file, []byte(text))
}

// Remove deletes a page, keeping it in history/.
func (w *Wiki) Remove(id, why string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.remove(id); err != nil {
		return err
	}
	if why != "" {
		w.appendLog(why, []string{id})
	}
	w.rebuildIndex()
	return nil
}

func (w *Wiki) remove(id string) error {
	if !validID(id) {
		return fmt.Errorf("not a page id: %q", id)
	}
	w.snapshot(id)
	err := os.Remove(w.pageFile(id))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// snapshot copies a page into history/ before it changes.
func (w *Wiki) snapshot(id string) {
	b, err := os.ReadFile(w.pageFile(id))
	if err != nil {
		return
	}
	dir := filepath.Join(w.Dir, "history", strings.ReplaceAll(id, "/", "__"))
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, time.Now().UTC().Format("20060102T150405.000000000")+".md"), b, 0o644)
}

// History lists the earlier versions of a page, newest first.
func (w *Wiki) History(id string) []string {
	files, _ := filepath.Glob(filepath.Join(w.Dir, "history", strings.ReplaceAll(id, "/", "__"), "*.md"))
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	return files
}

func writeAtomic(file string, b []byte) error {
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// RawFile is one document in raw/.
type RawFile struct {
	Name   string `json:"name"` // path under raw/, with forward slashes
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Raw lists the documents.
func (w *Wiki) Raw() []RawFile {
	var out []RawFile
	root := w.RawDir()
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		sum := sha256.Sum256(b)
		out = append(out, RawFile{Name: filepath.ToSlash(rel), Size: int64(len(b)), SHA256: hex.EncodeToString(sum[:])})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// CleanRawName makes a name safe to keep under raw/: relative, forward
// slashes, nothing that climbs out.
func CleanRawName(name string) (string, error) {
	name = strings.TrimSpace(filepath.ToSlash(name))
	name = strings.TrimLeft(path.Clean("/"+name), "/")
	if name == "" || name == "." || strings.Contains(name, ":") {
		return "", fmt.Errorf("not a file name: %q", name)
	}
	return name, nil
}

// TextOnly reports whether b is a document a model can read as it is:
// UTF-8 text without NULs. PDF, Word and images are not, yet.
func TextOnly(b []byte) bool {
	if len(b) > 0 && strings.IndexByte(string(b[:min(len(b), 8000)]), 0) >= 0 {
		return false
	}
	return utf8.Valid(b)
}

// AddRaw keeps a document under raw/name, replacing one of the same name.
func (w *Wiki) AddRaw(name string, b []byte) (string, error) {
	name, err := CleanRawName(name)
	if err != nil {
		return "", err
	}
	if len(b) > MaxRawBytes {
		return "", fmt.Errorf("%s is %d bytes, over the %d limit", name, len(b), MaxRawBytes)
	}
	if !TextOnly(b) {
		return "", fmt.Errorf("%s is not text: convert it to Markdown first", name)
	}
	if err := w.Init(); err != nil {
		return "", err
	}
	file := filepath.Join(w.RawDir(), filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return "", err
	}
	return name, writeAtomic(file, b)
}

// Note is something put into the wiki by hand or by the agent rather than
// as a file: a passage of a conversation, a finding, a decision.
type Note struct {
	Title   string
	Content string
	// From says where it came from, for the page's sources and for whoever
	// reads the document later: "conversation «x» (id), message 12".
	From string
}

// AddNote keeps a note as a document under notes/, named by when and what,
// so the next ingest reads it into pages — a new page, or more on one that
// is there.
func (w *Wiki) AddNote(n Note) (string, error) {
	content := strings.TrimSpace(n.Content)
	if content == "" {
		return "", errors.New("nothing to add: the note is empty")
	}
	title := strings.Join(strings.Fields(n.Title), " ")
	if title == "" {
		title = firstLine(content)
	}
	var b strings.Builder
	b.WriteString("# " + title + "\n\n")
	if n.From != "" {
		b.WriteString("> Added to the wiki from " + n.From + ", " + time.Now().Format("2006-01-02 15:04") + ".\n\n")
	}
	b.WriteString(content + "\n")
	slug := Slugify(title)
	if r := []rune(slug); len(r) > 60 {
		slug = strings.TrimRight(string(r[:60]), "-")
	}
	return w.AddRaw("notes/"+time.Now().Format("20060102-150405")+"-"+slug+".md", []byte(b.String()))
}

// firstLine is a note's first words, for a title when none was given.
func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	line = strings.Trim(strings.TrimSpace(line), "#>*-_ ")
	if r := []rune(line); len(r) > 70 {
		line = string(r[:70]) + "…"
	}
	if line == "" {
		line = "Note"
	}
	return line
}

// ReadRaw reads a document.
func (w *Wiki) ReadRaw(name string) (string, error) {
	name, err := CleanRawName(name)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(filepath.Join(w.RawDir(), filepath.FromSlash(name)))
	return string(b), err
}

// RemoveRaw deletes a document; the next ingest removes what came only from
// it.
func (w *Wiki) RemoveRaw(name string) error {
	name, err := CleanRawName(name)
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(w.RawDir(), filepath.FromSlash(name)))
}

// Source is what the wiki knows of one raw file.
type Source struct {
	SHA256   string    `json:"sha256"`
	Status   string    `json:"status"` // ingested or failed
	Error    string    `json:"error,omitempty"`
	Pages    []string  `json:"pages,omitempty"`
	Ingested time.Time `json:"ingested"`
}

// State is state.json.
type State struct {
	Version  int                `json:"version"`
	Sources  map[string]*Source `json:"sources"`
	Ingested time.Time          `json:"ingested,omitempty"`
}

// State reads state.json.
func (w *Wiki) State() State {
	st := State{Sources: map[string]*Source{}}
	if b, err := os.ReadFile(filepath.Join(w.Dir, "state.json")); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	if st.Sources == nil {
		st.Sources = map[string]*Source{}
	}
	return st
}

func (w *Wiki) saveState(st State) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(w.Dir, 0o755); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(w.Dir, "state.json"), b)
}

// Pending lists the raw files an ingest would read: new or changed since it
// last did.
func (w *Wiki) Pending() []string {
	st := w.State()
	var out []string
	for _, r := range w.Raw() {
		if s, ok := st.Sources[r.Name]; !ok || s.Status != "ingested" || s.SHA256 != r.SHA256 {
			out = append(out, r.Name)
		}
	}
	return out
}

// Stats sums the wiki up.
type Stats struct {
	Dir     string         `json:"dir"`
	Pages   int            `json:"pages"`
	ByType  map[string]int `json:"by_type"`
	Raw     int            `json:"raw"`
	Pending int            `json:"pending"`
	Failed  int            `json:"failed"`
	Version int            `json:"version"`
	// Ingested is when the last ingest finished.
	Ingested time.Time `json:"ingested,omitempty"`
}

// Stats reads the numbers.
func (w *Wiki) Stats() Stats {
	s := Stats{Dir: w.Dir, ByType: map[string]int{}}
	for _, p := range w.Pages() {
		s.Pages++
		s.ByType[p.Type]++
	}
	st := w.State()
	s.Raw, s.Pending, s.Version, s.Ingested = len(w.Raw()), len(w.Pending()), st.Version, st.Ingested
	for _, src := range st.Sources {
		if src.Status == "failed" {
			s.Failed++
		}
	}
	return s
}
