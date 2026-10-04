package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/phanngoc/agent-tui/internal/config"
)

// DefaultAddr is where `agent-tui serve` listens unless told otherwise.
const DefaultAddr = "127.0.0.1:7788"

// Info is the gateway's discovery file, written while it runs.
type Info struct {
	Addr    string    `json:"addr"`
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
	Version string    `json:"version"`
}

func infoPath() string { return filepath.Join(config.DataDir(), "gateway.json") }

// WriteInfo announces a running gateway.
func WriteInfo(i Info) error { return config.WriteJSON(infoPath(), i) }

// RemoveInfo withdraws it, if it is still ours.
func RemoveInfo(pid int) {
	if i, ok := ReadInfo(); ok && i.PID == pid {
		_ = os.Remove(infoPath())
	}
}

// ReadInfo reads the discovery file.
func ReadInfo() (Info, bool) {
	b, err := os.ReadFile(infoPath())
	if err != nil {
		return Info{}, false
	}
	var i Info
	if json.Unmarshal(b, &i) != nil || i.Addr == "" {
		return Info{}, false
	}
	return i, true
}

// Alive checks that a gateway answers at addr.
func Alive(addr string) bool {
	c := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := c.Get("http://" + addr + "/api/health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Find returns the address of the running gateway of this data folder. It
// does not probe the default port: a gateway there may belong to another
// data folder, and joining it would show sessions it cannot read.
func Find() (string, bool) {
	if i, ok := ReadInfo(); ok && Alive(i.Addr) {
		return i.Addr, true
	}
	return "", false
}

// Start launches `agent-tui serve` in the background, detached from this
// process so it outlives it, with its output in the data directory.
func Start() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(config.DataDir(), "gateway.log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(self, "serve")
	cmd.Stdout, cmd.Stderr = logf, logf
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// Client is a peer's connection to the gateway. It reconnects on its own,
// and while it is disconnected Publish keeps what it can and drops the rest:
// a terminal never waits for the web.
type Client struct {
	Kind, Root string
	Autostart  bool

	out  chan Event
	cmds chan Command

	mu       sync.Mutex
	sessions []string
	held     chan struct{}

	id        atomic.Value // string
	connected atomic.Bool
	addr      atomic.Value // string
	ctx       context.Context
	cancel    context.CancelFunc
	started   bool
}

// Join connects in the background and returns at once.
func Join(kind, root string, autostart bool) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{Kind: kind, Root: root, Autostart: autostart,
		out: make(chan Event, 8192), cmds: make(chan Command, 64), held: make(chan struct{}, 1),
		ctx: ctx, cancel: cancel}
	go c.loop()
	return c
}

// Publish queues an event for the gateway.
func (c *Client) Publish(e Event) {
	if c == nil {
		return
	}
	select {
	case c.out <- e:
	default:
	}
}

// Commands delivers what the gateway asks this process to do.
func (c *Client) Commands() <-chan Command { return c.cmds }

// Hold says which sessions this process has open, so the gateway routes
// their commands here.
func (c *Client) Hold(ids []string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.sessions = append([]string(nil), ids...)
	c.mu.Unlock()
	select {
	case c.held <- struct{}{}:
	default:
	}
}

// Connected reports whether the gateway is reachable now.
func (c *Client) Connected() bool { return c != nil && c.connected.Load() }

// Addr is the gateway's address, once found.
func (c *Client) Addr() string {
	if c == nil {
		return ""
	}
	s, _ := c.addr.Load().(string)
	return s
}

// Close disconnects.
func (c *Client) Close() {
	if c != nil {
		c.cancel()
	}
}

func (c *Client) loop() {
	delay := time.Second
	for c.ctx.Err() == nil {
		err := c.session()
		c.connected.Store(false)
		if c.ctx.Err() != nil {
			return
		}
		_ = err
		select {
		case <-time.After(delay):
		case <-c.ctx.Done():
			return
		}
		delay = min(delay*2, 10*time.Second)
	}
}

// session is one connection, from registration until something breaks.
func (c *Client) session() error {
	addr, ok := Find()
	if !ok && c.Autostart && !c.started {
		c.started = true
		if Start() == nil {
			for i := 0; i < 40 && !ok; i++ {
				time.Sleep(250 * time.Millisecond)
				addr, ok = Find()
			}
		}
	}
	if !ok {
		return errors.New("no gateway")
	}
	c.addr.Store(addr)
	base := "http://" + addr
	var reg struct {
		ID string `json:"id"`
	}
	if err := post(c.ctx, base+"/api/gateway/peers", map[string]any{
		"kind": c.Kind, "root": c.Root, "pid": os.Getpid()}, &reg); err != nil {
		return err
	}
	c.id.Store(reg.ID)
	peer := base + "/api/gateway/peers/" + reg.ID

	ctx, cancel := context.WithCancel(c.ctx)
	defer cancel()
	errc := make(chan error, 3)

	// Commands: one long-lived stream.
	go func() {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, peer+"/commands", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			errc <- err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			errc <- fmt.Errorf("commands: http %d", resp.StatusCode)
			return
		}
		c.connected.Store(true)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64<<10), 8<<20)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			var cmd Command
			if json.Unmarshal([]byte(strings.TrimSpace(line[5:])), &cmd) == nil && cmd.Type != "" {
				select {
				case c.cmds <- cmd:
				case <-ctx.Done():
					return
				}
			}
		}
		errc <- errors.New("command stream closed")
	}()

	// Which sessions are held: now, and whenever it changes.
	go func() {
		send := func() error {
			c.mu.Lock()
			ids := append([]string{}, c.sessions...)
			c.mu.Unlock()
			return post(ctx, peer+"/sessions", map[string]any{"sessions": ids}, nil)
		}
		if err := send(); err != nil {
			errc <- err
			return
		}
		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-c.held:
			case <-tick.C:
			case <-ctx.Done():
				return
			}
			if err := send(); err != nil {
				errc <- err
				return
			}
		}
	}()

	// Events: batched, so a fast stream of deltas is a few requests a second.
	go func() {
		var batch []Event
		flush := time.NewTicker(60 * time.Millisecond)
		defer flush.Stop()
		for {
			select {
			case e := <-c.out:
				batch = append(batch, e)
				if len(batch) < 256 {
					continue
				}
			case <-flush.C:
			case <-ctx.Done():
				return
			}
			if len(batch) == 0 {
				continue
			}
			if err := post(ctx, peer+"/events", batch, nil); err != nil {
				errc <- err
				return
			}
			batch = batch[:0]
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-c.ctx.Done():
		return nil
	}
}

func post(ctx context.Context, url string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s: http %d", url, resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
