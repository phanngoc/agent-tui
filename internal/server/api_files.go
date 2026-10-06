package server

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/phanngoc/agent-tui/internal/gateway"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// The project explorer: a session's project folder, browsed and read from
// the web — on the host, or inside the WSL distribution it lives in, through
// the same filesystems the agent uses. Paths in and out are relative to the
// project and slash-separated, and none may leave it.

// maxReadBytes is the most of a file the explorer shows.
const maxReadBytes = 1 << 20

type projectFS struct {
	fs  vfs.FS
	dir string // the project folder in fs's own namespace
}

var (
	pfsMu sync.Mutex
	pfs   = map[string]projectFS{}
)

// filesOf is the filesystem a project's files are read through.
func filesOf(root string) (projectFS, error) {
	if root == "" {
		return projectFS{}, errors.New("which project?")
	}
	pfsMu.Lock()
	defer pfsMu.Unlock()
	if p, ok := pfs[root]; ok {
		return p, nil
	}
	p := projectFS{fs: vfs.NewLocal(root), dir: filepath.Clean(root)}
	if d, linux, ok := gateway.WSLPath(root); ok {
		p = projectFS{fs: vfs.NewWSL(d), dir: linux}
	}
	pfs[root] = p
	return p, nil
}

// join makes a project-relative path absolute in the filesystem, refusing
// one that climbs out of the project.
func (p projectFS) join(rel string) (string, error) {
	rel = strings.TrimPrefix(strings.ReplaceAll(rel, `\`, "/"), "/")
	clean := path.Clean("/" + rel)
	if clean == "/" {
		return p.dir, nil
	}
	if strings.Contains(clean, "/../") {
		return "", errors.New("outside the project")
	}
	if _, ok := p.fs.(*vfs.WSL); ok {
		return path.Join(p.dir, clean), nil
	}
	return filepath.Join(p.dir, filepath.FromSlash(clean)), nil
}

// rel is an absolute path in the filesystem as project-relative, or false
// when it is not inside the project.
func (p projectFS) rel(abs string) (string, bool) {
	if _, ok := p.fs.(*vfs.WSL); ok {
		abs = path.Clean(abs)
		if abs == p.dir {
			return "", true
		}
		r, ok := strings.CutPrefix(abs, strings.TrimSuffix(p.dir, "/")+"/")
		return r, ok
	}
	r, err := filepath.Rel(p.dir, filepath.Clean(abs))
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) || filepath.IsAbs(r) {
		return "", false
	}
	if r == "." {
		r = ""
	}
	return filepath.ToSlash(r), true
}

type fileEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Dir  bool   `json:"dir"`
	Size int64  `json:"size,omitempty"`
}

// hidden are entries the explorer leaves out: a repository's own database.
var hidden = map[string]bool{".git": true}

func (s *Server) fileRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/files/list", func(w http.ResponseWriter, r *http.Request) {
		p, err := filesOf(r.URL.Query().Get("root"))
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		rel := r.URL.Query().Get("dir")
		abs, err := p.join(rel)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		entries, err := p.fs.ReadDir(ctx, abs)
		if err != nil {
			fail(w, http.StatusNotFound, err)
			return
		}
		out := []fileEntry{}
		for _, e := range entries {
			if hidden[e.Name] {
				continue
			}
			out = append(out, fileEntry{Name: e.Name, Path: strings.TrimPrefix(path.Join(rel, e.Name), "/"), Dir: e.Dir, Size: e.Size})
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Dir != out[j].Dir {
				return out[i].Dir
			}
			return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
		})
		writeJSON(w, map[string]any{"dir": rel, "entries": out})
	})

	m.HandleFunc("GET /api/files/read", func(w http.ResponseWriter, r *http.Request) {
		p, err := filesOf(r.URL.Query().Get("root"))
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		rel := r.URL.Query().Get("path")
		abs, err := p.join(rel)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		st, err := p.fs.Stat(ctx, abs)
		if err != nil {
			fail(w, http.StatusNotFound, err)
			return
		}
		if st.Dir {
			fail(w, http.StatusBadRequest, errors.New("that is a folder"))
			return
		}
		// The editor asks for more than the viewer: a file it opens cut short
		// would be saved cut short.
		limit := int64(maxReadBytes)
		if n, err := strconv.ParseInt(r.URL.Query().Get("max"), 10, 64); err == nil && n > 0 {
			limit = min(n, 50<<20)
		}
		data, truncated, err := p.fs.ReadFile(ctx, abs, limit)
		if err != nil {
			fail(w, http.StatusNotFound, err)
			return
		}
		binary := !utf8.Valid(data) && !truncated || strings.ContainsRune(string(data[:min(len(data), 8000)]), 0)
		out := map[string]any{"path": strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(rel, `\`, "/")), "/"),
			"size": st.Size, "truncated": truncated, "binary": binary, "modified": time.Unix(0, st.ModNano).UTC(),
			"mtime": mtimeOf(st)} // exact, for the editor's save check
		if !binary {
			out["text"] = string(data)
		}
		writeJSON(w, out)
	})

	// raw serves a file as it is, for images and the like.
	m.HandleFunc("GET /api/files/raw", func(w http.ResponseWriter, r *http.Request) {
		p, err := filesOf(r.URL.Query().Get("root"))
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		rel := r.URL.Query().Get("path")
		abs, err := p.join(rel)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		data, _, err := p.fs.ReadFile(ctx, abs, 32<<20)
		if err != nil {
			fail(w, http.StatusNotFound, err)
			return
		}
		ct := mime.TypeByExtension(path.Ext(rel))
		if ct == "" {
			ct = http.DetectContentType(data)
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "sandbox")
		_, _ = w.Write(data)
	})

	// find lists project files whose path holds every word of q, shortest
	// first: the explorer's quick open.
	m.HandleFunc("GET /api/files/find", func(w http.ResponseWriter, r *http.Request) {
		p, err := filesOf(r.URL.Query().Get("root"))
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		words := strings.Fields(strings.ToLower(r.URL.Query().Get("q")))
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		files, err := p.fs.ListFiles(ctx, p.dir, 60000)
		if err != nil {
			fail(w, http.StatusNotFound, err)
			return
		}
		var out []string
		for _, f := range files {
			lf := strings.ToLower(f)
			ok := len(words) > 0
			for _, wd := range words {
				if !strings.Contains(lf, wd) {
					ok = false
					break
				}
			}
			if ok {
				out = append(out, f)
			}
		}
		sort.Slice(out, func(i, j int) bool { return len(out[i]) < len(out[j]) || len(out[i]) == len(out[j]) && out[i] < out[j] })
		if len(out) > 50 {
			out = out[:50]
		}
		writeJSON(w, map[string]any{"files": nz(out)})
	})

	// resolve finds the file a path in a conversation means: relative to the
	// project or the session's folder, absolute inside the project in either
	// spelling, or — failing those — the project file it is the end of.
	m.HandleFunc("GET /api/files/resolve", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		p, err := filesOf(q.Get("root"))
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		want := strings.TrimSpace(q.Get("p"))
		want = strings.Trim(want, "`'\"()[]<>,;")
		if i := strings.LastIndex(want, ":"); i > 1 { // file.go:12 or file.go:12:3
			if _, err := parseLine(want[i+1:]); err == nil {
				want = want[:i]
				if j := strings.LastIndex(want, ":"); j > 1 {
					if _, err := parseLine(want[j+1:]); err == nil {
						want = want[:j]
					}
				}
			}
		}
		if want == "" {
			fail(w, http.StatusBadRequest, errors.New("which path?"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		found := func(rel string) {
			writeJSON(w, map[string]any{"path": rel})
		}
		var tries []string
		// Absolute: a host path, a WSL UNC path or a Linux path in the project.
		if d, linux, ok := gateway.WSLPath(want); ok {
			if _, isWSL := p.fs.(*vfs.WSL); isWSL && d != "" {
				want = linux
			}
		}
		if rel, ok := p.rel(want); ok && (strings.HasPrefix(want, "/") || filepath.IsAbs(want)) {
			tries = append(tries, rel)
		} else {
			tries = append(tries, want)
			if cwd := q.Get("cwd"); cwd != "" {
				if rel, ok := p.rel(cwd); ok && rel != "" {
					tries = append(tries, path.Join(rel, want))
				}
			}
		}
		for _, t := range tries {
			if abs, err := p.join(t); err == nil {
				if st, err := p.fs.Stat(ctx, abs); err == nil && !st.Dir {
					found(strings.TrimPrefix(path.Clean("/"+t), "/"))
					return
				}
			}
		}
		// The end of a project file's path: "sequence.md", "server/api.go".
		files, err := p.fs.ListFiles(ctx, p.dir, 60000)
		if err != nil {
			fail(w, http.StatusNotFound, err)
			return
		}
		tail := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(want, `\`, "/")), "/")
		var cands []string
		for _, f := range files {
			if f == tail || strings.HasSuffix(f, "/"+tail) {
				cands = append(cands, f)
			}
		}
		sort.Slice(cands, func(i, j int) bool { return len(cands[i]) < len(cands[j]) })
		if len(cands) == 1 {
			found(cands[0])
			return
		}
		if len(cands) > 20 {
			cands = cands[:20]
		}
		writeJSON(w, map[string]any{"candidates": nz(cands)})
	})
}

func parseLine(s string) (int, error) {
	n := 0
	if s == "" {
		return 0, errors.New("empty")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("not a line")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}
