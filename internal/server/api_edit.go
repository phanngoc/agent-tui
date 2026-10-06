package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/phanngoc/agent-tui/internal/git"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// The editor's half of the file API: writing, with a check that nobody
// changed the file since it was read; creating, renaming and deleting; the
// whole file list for quick open; content search; and git's view of the
// project for source control and diffs. Like the explorer, it works through
// the project's own filesystem, so a project in WSL is edited inside it.

// mtimeOf is a file's modification time as the editor compares it: in
// microseconds, since nanoseconds since 1970 are past the integers a
// JavaScript number holds exactly, and a rounded time never matches.
func mtimeOf(st vfs.FileInfo) int64 { return st.ModNano / 1000 }

// run executes a command in the project, for what the filesystem interface
// does not cover (moving, removing) on a filesystem that is not this one.
func (p projectFS) run(ctx context.Context, name string, args ...string) error {
	cmd := p.fs.Command(ctx, p.dir, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}

func (p projectFS) local() bool { return p.fs.IsLocal() }

func (s *Server) editRoutes(m *http.ServeMux) {
	// write saves a file. base is the modification time the editor read it
	// at; a file changed since is not overwritten unless force says so.
	m.HandleFunc("PUT /api/files/write", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Root  string `json:"root"`
			Path  string `json:"path"`
			Text  string `json:"text"`
			Base  int64  `json:"base"`
			Force bool   `json:"force"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		p, err := filesOf(in.Root)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		abs, err := p.join(in.Path)
		if err != nil || in.Path == "" {
			fail(w, http.StatusBadRequest, errors.New("which file?"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if in.Base != 0 && !in.Force {
			if st, err := p.fs.Stat(ctx, abs); err == nil && mtimeOf(st) != in.Base {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				fmt.Fprintf(w, `{"error":"the file changed on disk since it was opened","modified":%d}`, mtimeOf(st))
				return
			}
		}
		if err := p.fs.WriteFile(ctx, abs, []byte(in.Text)); err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		st, _ := p.fs.Stat(ctx, abs)
		writeJSON(w, map[string]any{"path": in.Path, "modified": mtimeOf(st), "size": st.Size})
	})

	// stat reports modification times, for the editor to notice files that
	// changed under it.
	m.HandleFunc("POST /api/files/stat", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Root  string   `json:"root"`
			Paths []string `json:"paths"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		p, err := filesOf(in.Root)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		out := map[string]int64{}
		for _, rel := range in.Paths {
			if abs, err := p.join(rel); err == nil {
				if st, err := p.fs.Stat(ctx, abs); err == nil {
					out[rel] = mtimeOf(st)
				} else {
					out[rel] = -1 // gone
				}
			}
		}
		writeJSON(w, map[string]any{"modified": out})
	})

	m.HandleFunc("POST /api/files/create", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Root string `json:"root"`
			Path string `json:"path"`
			Dir  bool   `json:"dir"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		p, err := filesOf(in.Root)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		abs, err := p.join(in.Path)
		if err != nil || strings.Trim(in.Path, "/") == "" {
			fail(w, http.StatusBadRequest, errors.New("which path?"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if _, err := p.fs.Stat(ctx, abs); err == nil {
			fail(w, http.StatusConflict, errors.New(in.Path+" already exists"))
			return
		}
		switch {
		case in.Dir && p.local():
			err = os.MkdirAll(abs, 0o755)
		case in.Dir:
			err = p.run(ctx, "mkdir", "-p", "--", abs)
		default:
			err = p.fs.WriteFile(ctx, abs, nil)
		}
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]any{"path": in.Path})
	})

	m.HandleFunc("POST /api/files/rename", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Root string `json:"root"`
			From string `json:"from"`
			To   string `json:"to"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		p, err := filesOf(in.Root)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		from, err1 := p.join(in.From)
		to, err2 := p.join(in.To)
		if err1 != nil || err2 != nil || strings.Trim(in.From, "/") == "" || strings.Trim(in.To, "/") == "" {
			fail(w, http.StatusBadRequest, errors.New("from and to, inside the project"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if _, err := p.fs.Stat(ctx, to); err == nil {
			fail(w, http.StatusConflict, errors.New(in.To+" already exists"))
			return
		}
		if p.local() {
			if err = os.MkdirAll(filepath.Dir(to), 0o755); err == nil {
				err = os.Rename(from, to)
			}
		} else {
			if err = p.run(ctx, "mkdir", "-p", "--", path.Dir(to)); err == nil {
				err = p.run(ctx, "mv", "--", from, to)
			}
		}
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]any{"path": in.To})
	})

	m.HandleFunc("POST /api/files/delete", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Root string `json:"root"`
			Path string `json:"path"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		p, err := filesOf(in.Root)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		abs, err := p.join(in.Path)
		if err != nil || strings.Trim(in.Path, "/") == "" {
			fail(w, http.StatusBadRequest, errors.New("the project itself is not deleted from here"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if p.local() {
			err = os.RemoveAll(abs)
		} else {
			err = p.run(ctx, "rm", "-rf", "--", abs)
		}
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// all is every project file, for quick open to match on in the page.
	m.HandleFunc("GET /api/files/all", func(w http.ResponseWriter, r *http.Request) {
		p, err := filesOf(r.URL.Query().Get("root"))
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		files, err := p.fs.ListFiles(ctx, p.dir, 200000)
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]any{"files": nz(files), "truncated": len(files) >= 200000})
	})

	// search looks for text in the project's files.
	m.HandleFunc("GET /api/files/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		p, err := filesOf(q.Get("root"))
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if q.Get("q") == "" {
			writeJSON(w, map[string]any{"hits": []vfs.GrepHit{}})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		opts := vfs.GrepOptions{Query: q.Get("q"), Regex: q.Get("regex") == "1",
			CaseSensitive: q.Get("case") == "1", Limit: 2000, MaxFileBytes: 2 << 20}
		if p.local() {
			// The in-process scanner searches the files it is given: the
			// project's, past its ignore rules.
			if opts.Files, err = p.fs.ListFiles(ctx, p.dir, 200000); err != nil {
				fail(w, http.StatusInternalServerError, err)
				return
			}
		}
		hits, truncated, err := p.fs.Grep(ctx, p.dir, opts)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if inc := strings.TrimSpace(q.Get("include")); inc != "" {
			kept := hits[:0]
			for _, h := range hits {
				if matchGlobs(inc, h.Path) {
					kept = append(kept, h)
				}
			}
			hits = kept
		}
		writeJSON(w, map[string]any{"hits": nz(hits), "truncated": truncated})
	})

	// git/status is the project's branch and changed files.
	m.HandleFunc("GET /api/git/status", func(w http.ResponseWriter, r *http.Request) {
		p, err := filesOf(r.URL.Query().Get("root"))
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		prefix, ok := repoPrefix(ctx, p)
		if !ok {
			writeJSON(w, map[string]any{"repo": false})
			return
		}
		out, err := git.Run(ctx, p.fs, p.dir, "status", "--porcelain=v1", "-b", "-z", "--untracked-files=all")
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		type change struct {
			Path   string `json:"path"`
			Status string `json:"status"` // two letters, git's XY
			From   string `json:"from,omitempty"`
		}
		changes := []change{}
		branch := ""
		recs := strings.Split(string(out), "\x00")
		for i := 0; i < len(recs); i++ {
			rec := recs[i]
			if strings.HasPrefix(rec, "## ") {
				branch = strings.TrimPrefix(rec, "## ")
				continue
			}
			if len(rec) < 4 {
				continue
			}
			c := change{Status: rec[:2], Path: rec[3:]}
			if c.Status[0] == 'R' || c.Status[0] == 'C' {
				if i+1 < len(recs) {
					c.From = recs[i+1]
					i++
				}
			}
			// Paths are the repository's; the editor's are the project's.
			if rel, ok := strings.CutPrefix(c.Path, prefix); ok {
				c.Path = rel
				c.From = strings.TrimPrefix(c.From, prefix)
				changes = append(changes, c)
			}
		}
		sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
		writeJSON(w, map[string]any{"repo": true, "branch": branch, "changes": changes})
	})

	// git/head is a file as it is in HEAD, for the diff against it.
	m.HandleFunc("GET /api/git/head", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		p, err := filesOf(q.Get("root"))
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		rel := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(q.Get("path"), `\`, "/")), "/")
		if rel == "" || strings.HasPrefix(rel, "../") {
			fail(w, http.StatusBadRequest, errors.New("which file?"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		prefix, ok := repoPrefix(ctx, p)
		if !ok {
			fail(w, http.StatusNotFound, errors.New("not a git repository"))
			return
		}
		out, err := git.Run(ctx, p.fs, p.dir, "show", "HEAD:"+prefix+rel)
		if err != nil {
			writeJSON(w, map[string]any{"text": "", "new": true})
			return
		}
		writeJSON(w, map[string]any{"text": string(out)})
	})
}

// repoPrefix is where the project sits in its repository ("" at the top,
// "web/admin/" below it), as git says it — no path comparison, which the
// two spellings of a Windows folder would get wrong.
func repoPrefix(ctx context.Context, p projectFS) (string, bool) {
	out, err := git.Run(ctx, p.fs, p.dir, "rev-parse", "--show-prefix")
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// matchGlobs says a path matches one of a comma-separated list of globs
// ("*.go, web/**"). A glob without a slash matches the file's name.
func matchGlobs(list, p string) bool {
	for _, g := range strings.Split(list, ",") {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		if strings.HasSuffix(g, "/**") && strings.HasPrefix(p, strings.TrimSuffix(g, "**")) {
			return true
		}
		target := p
		if !strings.Contains(g, "/") {
			target = path.Base(p)
		}
		if ok, _ := path.Match(g, target); ok {
			return true
		}
	}
	return false
}
