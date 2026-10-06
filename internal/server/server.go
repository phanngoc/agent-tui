// Package server is `agent-tui serve`: the gateway's HTTP face. It serves the
// admin's JSON API, the event stream every web page listens to, the endpoints
// terminal apps connect to as peers, and — when it has been built — the admin
// itself.
//
// It listens on loopback only and refuses requests whose Host is not a
// loopback name, which is what stops a web page elsewhere from reaching it
// through DNS rebinding. There is no login: whoever can reach this port is
// already on this machine as this user.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/phanngoc/agent-tui/internal/config"
	"github.com/phanngoc/agent-tui/internal/gateway"
	"github.com/phanngoc/agent-tui/internal/learn"
	"github.com/phanngoc/agent-tui/internal/mcp"
	"github.com/phanngoc/agent-tui/internal/schedule"
	"github.com/phanngoc/agent-tui/internal/term"
)

// Server is the gateway process.
type Server struct {
	Hub     *gateway.Hub
	Runner  *gateway.Runner
	Cfg     config.Config
	Version string
	// WebDir, when set, is a static export of the admin to serve at /.
	WebDir string

	mux      *http.ServeMux
	started  time.Time
	stop     chan struct{}
	stopOnce sync.Once
	restart  atomic.Bool

	cacheMu sync.Mutex
	cache   map[string]cached

	loginsMu sync.Mutex
	logins   map[string]pendingLogin

	// Sched runs scheduled jobs, while the gateway serves.
	Sched     *schedule.Scheduler
	schedHost *schedHost
	// Terms are the web terminals.
	Terms *term.Manager
}

// pendingLogin is a sign-in to an MCP server that a page started.
type pendingLogin struct {
	login  *mcp.Login
	server mcp.Server
	root   string
}

type cached struct {
	mtime time.Time
	size  int64
	sum   gateway.Summary
}

// New assembles a server.
func New(cfg config.Config, version, webDir string) *Server {
	hub := gateway.NewHub()
	s := &Server{Hub: hub, Cfg: cfg, Version: version, WebDir: webDir, started: time.Now().UTC(),
		cache: map[string]cached{}, stop: make(chan struct{}), logins: map[string]pendingLogin{}}
	s.Runner = gateway.NewRunner(hub, cfg)
	s.Runner.Learner = learn.Default()
	hub.Local = s.Runner
	s.Terms = term.NewManager()
	s.schedHost = &schedHost{s: s, last: map[string]string{}}
	s.Sched = &schedule.Scheduler{Store: schedule.DefaultStore(), Host: s.schedHost,
		Notify: func(j *schedule.Job, r schedule.Run) {
			hub.Publish(gateway.Event{Type: EvSchedule, Session: r.Session, Root: j.Root,
				Data: mustJSON(map[string]any{"job": j.ID, "name": j.Name, "run": r})})
		},
		Changed: func() { s.changed("schedule", "") }}
	s.routes()
	return s
}

// ListenAndServe runs until the listener fails or Stop is called.
func (s *Server) ListenAndServe(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	_ = gateway.WriteInfo(gateway.Info{Addr: ln.Addr().String(), PID: os.Getpid(), Started: s.started, Version: s.Version})
	defer gateway.RemoveInfo(os.Getpid())
	go s.watchLearner()
	schedCtx, stopSched := context.WithCancel(context.Background())
	defer stopSched()
	go s.watchSchedule(schedCtx)
	go s.Sched.Run(schedCtx)
	log.Printf("agent-tui gateway on http://%s", ln.Addr())
	srv := &http.Server{Handler: s.guard(s.mux), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-s.stop:
		// Event streams never finish on their own, so the grace is short and
		// whatever is still open after it is closed.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
		_ = srv.Close()
		s.Shutdown()
		if s.restart.Load() {
			// The port and the discovery file are free now, so the new one
			// — from the binary on disk, which may be newer — can take them.
			if err := gateway.Start(); err != nil {
				log.Printf("agent-tui gateway: restart: %v", err)
			} else {
				log.Printf("agent-tui gateway stopped; a new one is starting")
				return nil
			}
		}
		log.Printf("agent-tui gateway stopped")
		return nil
	}
}

// Stop makes ListenAndServe stop serving, cancel the turns it runs and
// return.
func (s *Server) Stop() { s.stopOnce.Do(func() { close(s.stop) }) }

// Shutdown stops running turns and closes the terminals.
func (s *Server) Shutdown() {
	s.Runner.Shutdown()
	s.Terms.CloseAll()
	gateway.RemoveInfo(os.Getpid())
}

func loopbackHost(h string) bool {
	host, _, err := net.SplitHostPort(h)
	if err != nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func loopbackOrigin(o string) bool {
	if o == "" {
		return true
	}
	for _, p := range []string{"http://", "https://"} {
		if rest, ok := strings.CutPrefix(o, p); ok {
			return loopbackHost(rest)
		}
	}
	return false
}

// guard enforces loopback, and answers CORS for the admin's dev server.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		origin := r.Header.Get("Origin")
		if !loopbackOrigin(origin) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Last-Event-ID")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) routes() {
	m := http.NewServeMux()
	s.mux = m

	m.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "version": s.Version, "pid": os.Getpid(),
			"started": s.started, "peers": len(s.Hub.Peers()), "running": s.Runner.Running()})
	})
	m.HandleFunc("GET /api/overview", s.overview)
	m.HandleFunc("GET /api/events", s.events)
	m.HandleFunc("GET /api/live", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.Hub.LiveAll()) })
	m.HandleFunc("GET /api/projects", s.projects)

	s.gatewayRoutes(m)
	s.sessionRoutes(m)
	s.skillRoutes(m)
	s.mcpRoutes(m)
	s.memoryRoutes(m)
	s.settingsRoutes(m)
	s.fsRoutes(m)
	s.scheduleRoutes(m)
	s.fileRoutes(m)
	s.editRoutes(m)
	s.termRoutes(m)

	m.HandleFunc("/", s.static)
}

// static serves the exported admin, falling back to its index for routes it
// handles itself.
func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}
	if s.WebDir == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><meta charset=utf-8><title>agent-tui gateway</title>
<body style="font:15px system-ui;padding:40px;max-width:640px">
<h2>agent-tui gateway is running</h2>
<p>The admin has not been built into this gateway. Run it from the repository:</p>
<pre>cd web/admin && npm install && npm run dev</pre>
<p>then open <a href="http://localhost:3000">http://localhost:3000</a>. Or build it once with
<code>npm run build</code> and restart with <code>agent-tui serve -web web/admin/out</code>.</p>`)
		return
	}
	// Hashed build output and the versioned editor never change under a
	// name, so the browser keeps them: the editor's megabytes load once.
	if strings.HasPrefix(r.URL.Path, "/_next/static/") || strings.HasPrefix(r.URL.Path, "/monaco/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	clean := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
	try := []string{clean, clean + ".html", filepath.Join(clean, "index.html")}
	for _, p := range try {
		full := filepath.Join(s.WebDir, p)
		if st, err := os.Stat(full); err == nil && !st.IsDir() {
			http.ServeFile(w, r, full)
			return
		}
	}
	http.ServeFile(w, r, filepath.Join(s.WebDir, "index.html"))
}

// events is the server-sent event stream every page subscribes to.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	var after int64
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		fmt.Sscan(v, &after)
	} else if v := r.URL.Query().Get("after"); v != "" {
		fmt.Sscan(v, &after)
	}
	replay, ch, cancel := s.Hub.Subscribe(after)
	defer cancel()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "retry: 2000\nevent: hello\ndata: {\"seq\":%d}\n\n", s.Hub.Seq())
	send := func(e gateway.Event) bool {
		b, _ := json.Marshal(e)
		_, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", e.Seq, b)
		return err == nil
	}
	for _, e := range replay {
		if !send(e) {
			return
		}
	}
	fl.Flush()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return
			}
			if !send(e) {
				return
			}
			fl.Flush()
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// watchLearner turns new learner activity into events, so the memory pages
// update as learning happens.
func (s *Server) watchLearner() {
	l := learn.Default()
	var last time.Time
	if a := l.Activities(1); len(a) > 0 {
		last = a[0].At
	}
	for range time.Tick(2 * time.Second) {
		acts := l.Activities(50)
		for i := len(acts) - 1; i >= 0; i-- {
			if acts[i].At.After(last) {
				last = acts[i].At
				s.Hub.Publish(gateway.Event{Type: gateway.EvLearn, Root: acts[i].Root,
					Session: acts[i].Session, Data: mustJSON(acts[i])})
			}
		}
	}
}

// summaries reads every saved session, cached by modification time.
func (s *Server) summaries() []gateway.Summary {
	dir := filepath.Join(config.DataDir(), "sessions")
	entries, _ := os.ReadDir(dir)
	live := s.Hub.LiveAll()
	out := make([]gateway.Summary, 0, len(entries))
	seen := map[string]bool{}
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		seen[id] = true
		info, err := e.Info()
		if err != nil {
			continue
		}
		c, ok := s.cache[id]
		if !ok || !c.mtime.Equal(info.ModTime()) || c.size != info.Size() {
			sess, err := gateway.Load(id)
			if err != nil {
				continue
			}
			c = cached{mtime: info.ModTime(), size: info.Size(), sum: gateway.SummaryOf(sess)}
			s.cache[id] = c
		}
		sum := c.sum
		if l, ok := live[id]; ok {
			sum.Busy, sum.Status = l.Busy, l.Status
		}
		sum.Owner = s.Hub.Owner(id)
		out = append(out, sum)
	}
	for id := range s.cache {
		if !seen[id] {
			delete(s.cache, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}

// Project is a folder the agent has worked in.
type Project struct {
	Root     string    `json:"root"`
	Name     string    `json:"name"`
	Sessions int       `json:"sessions"`
	Updated  time.Time `json:"updated"`
	Exists   bool      `json:"exists"`
	Config   bool      `json:"config"` // has a .agent-tui folder
	Peers    []string  `json:"peers,omitempty"`
}

func (s *Server) projectList() []Project {
	by := map[string]*Project{}
	add := func(root string) *Project {
		if root == "" {
			return nil
		}
		p := by[root]
		if p == nil {
			p = &Project{Root: root, Name: filepath.Base(root), Exists: config.IsDir(root),
				Config: config.IsDir(config.ProjectDir(root))}
			by[root] = p
		}
		return p
	}
	for _, sum := range s.summaries() {
		p := add(sum.Root)
		p.Sessions++
		if sum.Updated.After(p.Updated) {
			p.Updated = sum.Updated
		}
	}
	add(config.LoadPrefs().LastRoot)
	for _, peer := range s.Hub.Peers() {
		if p := add(peer.Root); p != nil {
			p.Peers = append(p.Peers, peer.ID)
		}
	}
	out := make([]Project, 0, len(by))
	for _, p := range by {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if (len(out[i].Peers) > 0) != (len(out[j].Peers) > 0) {
			return len(out[i].Peers) > 0
		}
		return out[i].Updated.After(out[j].Updated)
	})
	return out
}

func (s *Server) projects(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.projectList()) }

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	sums := s.summaries()
	busy := 0
	for _, x := range sums {
		if x.Busy {
			busy++
		}
	}
	info, _ := gateway.ReadInfo()
	writeJSON(w, map[string]any{
		"version":  s.Version,
		"pid":      os.Getpid(),
		"addr":     info.Addr,
		"started":  s.started,
		"peers":    s.Hub.Peers(),
		"sessions": len(sums),
		"busy":     busy,
		"recent":   sums[:min(len(sums), 8)],
		"projects": s.projectList(),
		"learner":  learn.Default().Status(),
		"activity": learn.Default().Activities(15),
		"paths": map[string]string{
			"config":   config.Dir(),
			"data":     config.DataDir(),
			"sessions": filepath.Join(config.DataDir(), "sessions"),
		},
	})
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 8<<20))
	if err := dec.Decode(v); err != nil {
		return errors.New("bad request body: " + err.Error())
	}
	return nil
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// changed tells every page that configuration changed, so it refetches.
func (s *Server) changed(kind, root string) {
	s.Hub.Publish(gateway.Event{Type: gateway.EvConfig, Root: root, Data: mustJSON(map[string]string{"kind": kind})})
}
