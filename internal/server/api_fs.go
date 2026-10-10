package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/phanngoc/agent-tui/internal/gateway"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// The folder picker's API: where to start (drives, home, the WSL
// distributions' homes, the places recent conversations ran), and one
// folder's subfolders at a time. A WSL folder is reached through the
// \\wsl.localhost share, so the picker walks it like any other folder and a
// session created there runs inside the distribution.

// Place is a starting point in the picker.
type Place struct {
	Label  string `json:"label"`
	Path   string `json:"path"`
	Target string `json:"target,omitempty"`
	CWD    string `json:"cwd,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// Entry is one subfolder.
type Entry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Project bool   `json:"project,omitempty"` // has .git or .agent-tui
	Hidden  bool   `json:"hidden,omitempty"`
}

var wslHomes struct {
	sync.Mutex
	at     time.Time
	places []Place
}

// distroHomes finds each WSL distribution's home folder as Windows reaches
// it. It asks every distribution at once and caches the answer: starting a
// stopped distribution to ask takes seconds.
func distroHomes(ctx context.Context) []Place {
	if runtime.GOOS != "windows" {
		return nil
	}
	wslHomes.Lock()
	defer wslHomes.Unlock()
	if time.Since(wslHomes.at) < 10*time.Minute && wslHomes.places != nil {
		return wslHomes.places
	}
	distros := vfs.Distros(ctx)
	out := make([]Place, len(distros))
	var wg sync.WaitGroup
	for i, d := range distros {
		wg.Add(1)
		go func(i int, d vfs.Distro) {
			defer wg.Done()
			unc := `\\wsl.localhost\` + d.Name
			p := Place{Label: "WSL " + d.Name, Path: unc + `\`, Target: "wsl:" + d.Name, CWD: "/", Detail: strings.ToLower(d.State)}
			cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			c := exec.CommandContext(cctx, "wsl.exe", "-d", d.Name, "--", "sh", "-c", `printf %s "$HOME"`)
			c.Env = append(os.Environ(), "WSL_UTF8=1")
			if b, err := c.Output(); err == nil {
				if home := strings.TrimSpace(string(b)); strings.HasPrefix(home, "/") {
					p.Path = unc + strings.ReplaceAll(home, "/", `\`)
					p.CWD = home
				}
			}
			out[i] = p
		}(i, d)
	}
	wg.Wait()
	wslHomes.places, wslHomes.at = out, time.Now()
	return out
}

func drives() []Place {
	if runtime.GOOS != "windows" {
		return []Place{{Label: "/", Path: "/"}}
	}
	var out []Place
	for c := 'C'; c <= 'Z'; c++ {
		p := string(c) + `:\`
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			out = append(out, Place{Label: string(c) + ":", Path: p})
		}
	}
	return out
}

// recentPlaces are where the latest conversations ran, newest first, each
// once.
func (s *Server) recentPlaces(limit int) []Place {
	seen := map[string]bool{}
	var out []Place
	for _, sum := range s.summaries() {
		target := sum.Target
		if target == "host" {
			target = ""
		}
		cwd := ""
		if target != "" {
			cwd = sum.CWD
		}
		// The same folder is one place, whichever host folder the terminal
		// that worked in it was opened from.
		key := strings.ToLower(sum.Root)
		if target != "" {
			key = target + "|" + cwd
		}
		if seen[key] || sum.Root == "" {
			continue
		}
		seen[key] = true
		label := filepath.Base(sum.Root)
		detail := sum.Root
		if target != "" {
			label = filepath.Base(filepath.FromSlash(cwd))
			detail = target + ":" + cwd
		}
		out = append(out, Place{Label: label, Path: sum.Root, Target: target, CWD: cwd, Detail: detail})
		if len(out) == limit {
			break
		}
	}
	return out
}

func (s *Server) fsRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/fs/roots", func(w http.ResponseWriter, r *http.Request) {
		home, _ := os.UserHomeDir()
		writeJSON(w, map[string]any{
			"home":   home,
			"drives": drives(),
			"wsl":    nz(distroHomes(r.Context())),
			"recent": nz(s.recentPlaces(10)),
		})
	})

	m.HandleFunc("GET /api/fs/list", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimSpace(r.URL.Query().Get("path"))
		hidden := r.URL.Query().Get("hidden") == "1"
		if path == "" {
			path, _ = os.UserHomeDir()
		}
		path = filepath.FromSlash(path)
		if !filepath.IsAbs(path) && !strings.HasPrefix(path, `\\`) {
			fail(w, http.StatusBadRequest, errors.New("give a full path"))
			return
		}
		path = filepath.Clean(path)
		ents, err := os.ReadDir(path)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		out := []Entry{}
		for _, e := range ents {
			if !e.IsDir() {
				// A link to a folder counts as one.
				if e.Type()&os.ModeSymlink == 0 {
					continue
				}
				if st, err := os.Stat(filepath.Join(path, e.Name())); err != nil || !st.IsDir() {
					continue
				}
			}
			isHidden := strings.HasPrefix(e.Name(), ".") || e.Name() == "$RECYCLE.BIN" || e.Name() == "System Volume Information"
			if isHidden && !hidden {
				continue
			}
			out = append(out, Entry{Name: e.Name(), Path: filepath.Join(path, e.Name()), Hidden: isHidden})
			if len(out) >= 2000 {
				break
			}
		}
		sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })

		// Mark the folders that are projects, which is what the reader is
		// looking for. Over a WSL share every stat is a round trip, so they
		// go in parallel, and a very long listing is not marked at all.
		if len(out) <= 400 {
			var wg sync.WaitGroup
			sem := make(chan struct{}, 16)
			for i := range out {
				wg.Add(1)
				sem <- struct{}{}
				go func(e *Entry) {
					defer wg.Done()
					defer func() { <-sem }()
					for _, mark := range []string{".git", ".agent-tui"} {
						if _, err := os.Stat(filepath.Join(e.Path, mark)); err == nil {
							e.Project = true
							return
						}
					}
				}(&out[i])
			}
			wg.Wait()
		}

		parent := filepath.Dir(path)
		if parent == path || strings.TrimRight(parent, `\`) == `\` || parent == `\\wsl.localhost` || parent == `\\wsl$` {
			parent = ""
		}
		resp := map[string]any{"path": path, "parent": parent, "entries": out, "sep": string(filepath.Separator)}
		if d, linux, ok := gateway.WSLPath(path); ok {
			resp["wsl"] = map[string]string{"distro": d, "linux": linux, "target": "wsl:" + d}
		}
		writeJSON(w, resp)
	})

	// POST /api/fs/mkdir makes a folder in the one the picker shows, to start a
	// project in: {parent, name}. A WSL folder is made through its share.
	m.HandleFunc("POST /api/fs/mkdir", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Parent string `json:"parent"`
			Name   string `json:"name"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		parent := filepath.Clean(filepath.FromSlash(strings.TrimSpace(in.Parent)))
		if !filepath.IsAbs(parent) && !strings.HasPrefix(parent, `\\`) {
			fail(w, http.StatusBadRequest, errors.New("give the full path of the folder to make it in"))
			return
		}
		name, err := folderName(in.Name)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if st, err := os.Stat(parent); err != nil || !st.IsDir() {
			fail(w, http.StatusBadRequest, errors.New("no folder "+parent))
			return
		}
		full := filepath.Join(parent, name)
		if err := os.Mkdir(full, 0o755); err != nil {
			if errors.Is(err, os.ErrExist) {
				fail(w, http.StatusConflict, errors.New(name+" is there already"))
				return
			}
			fail(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, map[string]any{"path": full})
	})
}

// folderName checks a new folder's name: one name, not a path, that every
// system the picker reaches — Windows, and Linux through a WSL share — takes.
func folderName(s string) (string, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return "", errors.New("name the folder")
	case s == "." || s == "..":
		return "", errors.New("not a folder name: " + s)
	case len(s) > 255:
		return "", errors.New("a folder name is at most 255 characters")
	case strings.ContainsAny(s, `/\<>:"|?*`):
		return "", errors.New(`a folder name cannot have / \ < > : " | ? *`)
	case strings.HasSuffix(s, ".") || strings.HasSuffix(s, " "):
		return "", errors.New("Windows does not take a folder name ending in a dot or a space")
	}
	for _, r := range s {
		if r < 32 {
			return "", errors.New("a folder name cannot have control characters")
		}
	}
	if base, _, _ := strings.Cut(strings.ToUpper(s), "."); base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" ||
		len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		return "", errors.New(s + " is a name Windows keeps for itself")
	}
	return s, nil
}
