package remote

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/phanngoc/agent-tui/internal/config"
)

// Tunnel states.
const (
	StateOff        = "off"
	StateInstalling = "installing"
	StateStarting   = "starting"
	StateUp         = "up"
	StateError      = "error"
)

// Status is what the admin shows about the tunnel.
type Status struct {
	State string `json:"state"`
	// URL is the gateway's public address while the tunnel is up.
	URL   string `json:"url,omitempty"`
	Error string `json:"error,omitempty"`
	// Log is cloudflared's last lines, for when it does not come up.
	Log   []string  `json:"log,omitempty"`
	Since time.Time `json:"since,omitzero"`
	Mode  string    `json:"mode,omitempty"`
}

// Manager runs cloudflared for the gateway.
type Manager struct {
	mu     sync.Mutex
	st     Status
	cancel context.CancelFunc
	done   chan struct{}
	// OnChange is told whenever the status changes.
	OnChange func(Status)
}

// Status is the tunnel's state now.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.st
	if st.State == "" {
		st.State = StateOff
	}
	st.Log = append([]string(nil), st.Log...)
	return st
}

func (m *Manager) set(f func(*Status)) {
	m.mu.Lock()
	f(&m.st)
	st := m.st
	m.mu.Unlock()
	if m.OnChange != nil {
		m.OnChange(st)
	}
}

func (m *Manager) logLine(l string) {
	m.mu.Lock()
	m.st.Log = append(m.st.Log, l)
	if n := len(m.st.Log); n > 40 {
		m.st.Log = m.st.Log[n-40:]
	}
	m.mu.Unlock()
}

// Start runs the tunnel to the gateway at local (http://127.0.0.1:7788), in
// the background, replacing one already running.
func (m *Manager) Start(t Tunnel, local string) {
	m.Stop()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	m.mu.Lock()
	m.cancel, m.done = cancel, done
	m.st = Status{State: StateStarting, Since: time.Now(), Mode: modeOf(t)}
	m.mu.Unlock()
	m.set(func(*Status) {})
	go func() {
		defer close(done)
		err := m.run(ctx, t, local)
		if ctx.Err() != nil {
			m.set(func(s *Status) { *s = Status{State: StateOff} })
			return
		}
		if err == nil {
			err = errors.New("cloudflared stopped")
		}
		m.set(func(s *Status) { s.State, s.Error, s.URL = StateError, err.Error(), "" })
	}()
}

// Stop ends the tunnel, waiting for cloudflared to exit.
func (m *Manager) Stop() {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.cancel, m.done = nil, nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func modeOf(t Tunnel) string {
	switch {
	case t.Mode != "":
		return t.Mode
	case t.APIToken != "":
		return "api"
	case t.Token != "":
		return "token"
	}
	return "quick"
}

var quickURL = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

func (m *Manager) run(ctx context.Context, t Tunnel, local string) error {
	bin, err := Cloudflared(ctx, func() { m.set(func(s *Status) { s.State = StateInstalling }) })
	if err != nil {
		return err
	}
	m.set(func(s *Status) { s.State = StateStarting })
	// HTTP/2 over 443 rather than QUIC: offices and hotel networks block
	// the UDP QUIC needs (port 7844), and cloudflared then spends a minute
	// failing at it before it falls back.
	args := []string{"tunnel", "--no-autoupdate", "--protocol", "http2"}
	url := ""
	switch modeOf(t) {
	case "quick":
		args = append(args, "--url", local)
	case "token":
		if t.Token == "" || t.Hostname == "" {
			return errors.New("a named tunnel needs its token and the hostname it serves")
		}
		args = append(args, "run", "--token", t.Token)
		url = "https://" + t.Hostname
	case "api":
		tok, err := Provision(ctx, t, local)
		if err != nil {
			return err
		}
		args = append(args, "run", "--token", tok)
		url = "https://" + t.Hostname
	default:
		return fmt.Errorf("unknown tunnel mode %q", t.Mode)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	hideWindow(cmd)
	// cloudflared writes its log to stderr; the token is in its arguments,
	// never in what is kept of the log.
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			line := sc.Text()
			m.logLine(line)
			if u := quickURL.FindString(line); u != "" && url == "" {
				url = u
			}
			// The address is printed before the tunnel can carry anything;
			// it is up once Cloudflare has a connection registered.
			if url != "" && strings.Contains(line, "Registered tunnel connection") {
				u := url
				m.set(func(s *Status) {
					if s.State != StateUp {
						s.State, s.URL, s.Error = StateUp, u, ""
					}
				})
			}
		}
	}()
	err = cmd.Wait()
	pw.Close()
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("cloudflared: %v — %s", err, lastLine(m.Status().Log))
	}
	return err
}

func lastLine(l []string) string {
	for i := len(l) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(l[i]); s != "" {
			return s
		}
	}
	return ""
}

// Cloudflared finds cloudflared: on PATH, or where agent-tui keeps its own
// copy, downloaded there from Cloudflare's releases the first time it is
// wanted (installing is told when that starts).
func Cloudflared(ctx context.Context, installing func()) (string, error) {
	if p, err := exec.LookPath("cloudflared"); err == nil {
		return p, nil
	}
	name := "cloudflared"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	own := filepath.Join(config.DataDir(), "bin", name)
	if st, err := os.Stat(own); err == nil && st.Size() > 0 {
		return own, nil
	}
	asset := map[string]string{
		"windows/amd64": "cloudflared-windows-amd64.exe",
		"windows/arm64": "cloudflared-windows-amd64.exe",
		"linux/amd64":   "cloudflared-linux-amd64",
		"linux/arm64":   "cloudflared-linux-arm64",
	}[runtime.GOOS+"/"+runtime.GOARCH]
	if asset == "" {
		return "", fmt.Errorf("install cloudflared (brew install cloudflared, or https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/)")
	}
	if installing != nil {
		installing()
	}
	if err := download(ctx, "https://github.com/cloudflare/cloudflared/releases/latest/download/"+asset, own); err != nil {
		return "", fmt.Errorf("downloading cloudflared: %w", err)
	}
	return own, nil
}

func download(ctx context.Context, url, to string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	tmp := to + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, to)
}
