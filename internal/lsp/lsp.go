// Package lsp runs language servers for the web editor, the way opencode does
// for its agent: a server per language, started in the project's own
// environment, spoken to over stdio in JSON-RPC with LSP's Content-Length
// framing.
//
// The gateway does not interpret the protocol. Each editor connection gets a
// server of its own and the gateway pipes messages both ways — the page's
// LSP client speaks to the server, and Monaco shows what it says — so the
// gateway stays a transport and the server sees exactly one client.
//
// TypeScript comes first: typescript-language-server with the project's own
// TypeScript when it has one (node_modules/typescript), as opencode passes
// tsserver.path, else the one installed beside the server. Both are
// installed once, with npm, under the data folder. A project in a WSL
// distribution runs that same install with the distribution's node, through
// /mnt — it is plain JavaScript — so the distribution needs node and nothing
// else.
package lsp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/phanngoc/agent-tui/internal/config"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// Server is a language server agent-tui knows how to run.
type Server struct {
	ID         string
	Extensions []string
	Packages   []string // npm packages installed for it
}

// TypeScript is the first server: TS and JS, with JSX.
var TypeScript = Server{ID: "typescript", Extensions: []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".mts", ".cts"},
	Packages: []string{"typescript-language-server@6", "typescript@5"}}

// Servers lists them by id.
var Servers = map[string]Server{TypeScript.ID: TypeScript}

// installDir is where npm installs the servers, on the host.
func installDir() string { return filepath.Join(config.DataDir(), "lsp") }

var installMu sync.Mutex

// ensureInstalled installs a server's packages once.
func ensureInstalled(ctx context.Context, s Server) (string, error) {
	installMu.Lock()
	defer installMu.Unlock()
	dir := installDir()
	cli := filepath.Join(dir, "node_modules", "typescript-language-server", "lib", "cli.mjs")
	if _, err := os.Stat(cli); err == nil {
		return dir, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	npm := "npm"
	if p, err := exec.LookPath("npm"); err == nil {
		npm = p
	} else {
		return "", errors.New("npm is not installed on this machine; it is needed once, to install the TypeScript language server")
	}
	args := append([]string{"install", "--no-audit", "--no-fund", "--no-save", "--prefix", dir}, s.Packages...)
	cmd := exec.CommandContext(ctx, npm, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("installing %s: %v: %s", strings.Join(s.Packages, " "), err, tail(out.String(), 600))
	}
	if _, err := os.Stat(cli); err != nil {
		return "", fmt.Errorf("installed, but %s is missing", cli)
	}
	return dir, nil
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

// mntPath is where a WSL distribution sees a Windows path.
func mntPath(p string) string {
	if len(p) >= 3 && p[1] == ':' {
		return "/mnt/" + strings.ToLower(p[:1]) + "/" + strings.ReplaceAll(p[3:], `\`, "/")
	}
	return strings.ReplaceAll(p, `\`, "/")
}

// Session is one running server, connected to one editor.
type Session struct {
	ID      string
	Server  string
	Root    string // the project, as the admin names it
	RootURI string // the project in the server's own namespace, as a file URI
	Started time.Time
	// InitOptions are what the client should pass as initializationOptions:
	// the tsserver to use.
	InitOptions map[string]any

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	wmu    sync.Mutex
	out    chan []byte
	done   chan struct{}
	err    error
	stderr *bytes.Buffer
	once   sync.Once
}

// Spec says where to run a server: the project's filesystem and folder.
type Spec struct {
	Server string
	Root   string
	FS     vfs.FS
	Dir    string // the project folder in FS's namespace
}

// Start installs the server if it has to and runs it in the project.
func Start(ctx context.Context, sp Spec) (*Session, error) {
	srv, ok := Servers[sp.Server]
	if !ok {
		return nil, fmt.Errorf("no language server %q", sp.Server)
	}
	dir, err := ensureInstalled(ctx, srv)
	if err != nil {
		return nil, err
	}
	cli := filepath.Join(dir, "node_modules", "typescript-language-server", "lib", "cli.mjs")
	bundled := filepath.Join(dir, "node_modules", "typescript", "lib")

	_, wsl := sp.FS.(*vfs.WSL)
	var cmd *exec.Cmd
	tsdk := bundled
	if wsl {
		// The project's own TypeScript, if it has one, else the bundled one
		// through /mnt.
		tsdk = mntPath(bundled)
		if _, err := sp.FS.Stat(ctx, path.Join(sp.Dir, "node_modules", "typescript", "lib", "tsserver.js")); err == nil {
			tsdk = path.Join(sp.Dir, "node_modules", "typescript", "lib")
		}
		cmd = sp.FS.Command(context.Background(), sp.Dir, "node", mntPath(cli), "--stdio")
	} else {
		sp.Dir = longPath(sp.Dir)
		if _, err := os.Stat(filepath.Join(sp.Dir, "node_modules", "typescript", "lib", "tsserver.js")); err == nil {
			tsdk = filepath.Join(sp.Dir, "node_modules", "typescript", "lib")
		}
		node, err := exec.LookPath("node")
		if err != nil {
			return nil, errors.New("node is not installed on this machine")
		}
		cmd = exec.Command(node, cli, "--stdio")
		cmd.Dir = sp.Dir
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting the %s language server: %w", srv.ID, err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	s := &Session{ID: hex.EncodeToString(b), Server: srv.ID, Root: sp.Root, RootURI: fileURI(sp.Dir, wsl), Started: time.Now(),
		// Typings acquisition runs npm in the background for every
		// dependency without types; the editor does not need it, and on
		// Windows it is one more process to fail.
		InitOptions: map[string]any{"tsserver": map[string]any{"path": joinNS(tsdk, "tsserver.js", wsl)}, "disableAutomaticTypingAcquisition": true,
			// A rename renames the symbol everywhere, not the local alias
			// (TypeScript's default, which turns a rename at a use into
			// "greet as hello"); completion offers exports from any module
			// and adds the import.
			"preferences": map[string]any{
				"providePrefixAndSuffixTextForRename":      false,
				"allowRenameOfImportPath":                  true,
				"includeCompletionsForModuleExports":       true,
				"includeCompletionsForImportStatements":    true,
				"includeCompletionsWithSnippetText":        true,
				"includeAutomaticOptionalChainCompletions": true,
				"importModuleSpecifierPreference":          "shortest",
			}},
		cmd: cmd, stdin: stdin, out: make(chan []byte, 512), done: make(chan struct{}), stderr: &stderr}
	go s.read(stdout)
	go func() {
		err := cmd.Wait()
		s.once.Do(func() {
			s.err = err
			close(s.done)
		})
	}()
	return s, nil
}

func joinNS(dir, name string, wsl bool) string {
	if wsl {
		return path.Join(dir, name)
	}
	return filepath.Join(dir, name)
}

// fileURI is a folder as a file URI in its own namespace: file:///home/x for
// Linux, file:///C:/x for Windows.
func fileURI(dir string, linux bool) string {
	p := strings.ReplaceAll(dir, `\`, "/")
	if !linux && !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "file://" + escapePath(strings.TrimSuffix(p, "/"))
}

// escapePath percent-encodes what a URI path cannot hold, as VS Code writes
// file URIs, keeping / and the drive's colon readable to servers.
func escapePath(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.IndexByte("/-._~:@!$&'()*+,;=", c) >= 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// read splits the server's output into messages.
func (s *Session) read(r io.Reader) {
	br := bufio.NewReaderSize(r, 64<<10)
	defer close(s.out)
	for {
		n := -1
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			if v, ok := strings.CutPrefix(strings.ToLower(line), "content-length:"); ok {
				n, _ = strconv.Atoi(strings.TrimSpace(v))
			}
		}
		if n < 0 {
			continue
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(br, body); err != nil {
			return
		}
		select {
		case s.out <- body:
		case <-s.done:
			return
		}
	}
}

// Messages is what the server sends, one JSON message each; it closes when
// the server ends.
func (s *Session) Messages() <-chan []byte { return s.out }

// Done closes when the server has exited.
func (s *Session) Done() <-chan struct{} { return s.done }

// Send writes one JSON message to the server.
func (s *Session) Send(msg []byte) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := fmt.Fprintf(s.stdin, "Content-Length: %d\r\n\r\n", len(msg)); err != nil {
		return err
	}
	_, err := s.stdin.Write(msg)
	return err
}

// Err says why the server ended, with what it last wrote to stderr.
func (s *Session) Err() string {
	msg := tail(s.stderr.String(), 800)
	if s.err != nil {
		return strings.TrimSpace(s.err.Error() + " " + msg)
	}
	return msg
}

// Close stops the server.
func (s *Session) Close() {
	_ = s.stdin.Close()
	go func() {
		select {
		case <-s.done:
		case <-time.After(3 * time.Second):
			if s.cmd.Process != nil {
				_ = s.cmd.Process.Kill()
			}
		}
	}()
}

// Manager keeps the running servers.
type Manager struct {
	mu sync.Mutex
	m  map[string]*Session
}

// NewManager makes an empty one.
func NewManager() *Manager { return &Manager{m: map[string]*Session{}} }

// Add keeps a session.
func (m *Manager) Add(s *Session) {
	m.mu.Lock()
	m.m[s.ID] = s
	m.mu.Unlock()
}

// Get finds one.
func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.m[id]
	return s, ok
}

// Close stops one and forgets it.
func (m *Manager) Close(id string) {
	m.mu.Lock()
	s := m.m[id]
	delete(m.m, id)
	m.mu.Unlock()
	if s != nil {
		s.Close()
	}
}

// CloseAll stops every server, for shutdown.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.m))
	for id := range m.m {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Close(id)
	}
}

// Check says, quickly and without installing anything, whether a server can
// run for a project: node where the project is, and the server installed or
// npm on this machine to install it. The editor asks before it opens its first
// file, to decide whether Monaco's own per-file TypeScript stays on.
func Check(ctx context.Context, sp Spec) error {
	if _, ok := Servers[sp.Server]; !ok {
		return fmt.Errorf("no language server %q", sp.Server)
	}
	cli := filepath.Join(installDir(), "node_modules", "typescript-language-server", "lib", "cli.mjs")
	if _, err := os.Stat(cli); err != nil {
		if _, err := exec.LookPath("npm"); err != nil {
			return errors.New("npm is not installed on this machine; it is needed once, to install the TypeScript language server")
		}
	}
	if _, wsl := sp.FS.(*vfs.WSL); wsl {
		cmd := sp.FS.Command(ctx, sp.Dir, "sh", "-c", "command -v node")
		if out, err := cmd.Output(); err != nil || strings.TrimSpace(string(out)) == "" {
			return errors.New("node is not installed in the distribution")
		}
		return nil
	}
	if _, err := exec.LookPath("node"); err != nil {
		return errors.New("node is not installed on this machine")
	}
	return nil
}
