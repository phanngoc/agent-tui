package gateway

// The gateway is a service of its own. Nothing has to be open for it to run,
// and it has to be open for nothing else to run: the terminal works without
// it, the web is served by it, and either can start it. These are the
// controls every one of them uses — `agent-tui gateway …`, the Start menu's
// web entry, a terminal joining, the installer.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/phanngoc/agent-tui/internal/config"
)

// startWait is how long a started gateway gets to answer.
const startWait = 10 * time.Second

// A gateway stopped on purpose stays stopped: terminals that are open, or
// open later, do not start it again behind the back of whoever stopped it.
// Starting it on purpose — `agent-tui gateway start`, `agent-tui web`, /web
// in a terminal — lifts that.
func stoppedPath() string { return filepath.Join(config.DataDir(), "gateway.stopped") }

// StoppedOnPurpose says the gateway was last stopped by `agent-tui gateway
// stop`, so terminals should not start it on their own.
func StoppedOnPurpose() bool {
	_, err := os.Stat(stoppedPath())
	return err == nil
}

// MarkStopped records a stop made on purpose. The gateway does it as it
// stops, when asked to hold.
func MarkStopped() error {
	return os.WriteFile(stoppedPath(), []byte(time.Now().UTC().Format(time.RFC3339)), 0o600)
}

// ClearStopped lifts a stop made on purpose: the gateway is being started on
// purpose.
func ClearStopped() { _ = os.Remove(stoppedPath()) }

// Ensure returns the running gateway's address, starting one in the
// background when there is none. started says this call started it. It is
// for starting on purpose, so it lifts a stop made on purpose.
func Ensure() (addr string, started bool, err error) {
	ClearStopped()
	if a, ok := Find(); ok {
		return a, false, nil
	}
	if err := Start(); err != nil {
		return "", false, fmt.Errorf("start the gateway: %w", err)
	}
	if a, ok := waitFor(startWait); ok {
		return a, true, nil
	}
	return "", false, errors.New("the gateway did not answer after starting; see gateway.log in the data folder")
}

func waitFor(d time.Duration) (string, bool) {
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
		if a, ok := Find(); ok {
			return a, true
		}
	}
	return "", false
}

// BusyError is a stop refused because the gateway is running turns, which
// stopping would cancel.
type BusyError struct{ Sessions []string }

func (e *BusyError) Error() string {
	return fmt.Sprintf("the gateway is running %d turn(s); stopping cancels them (use -force)", len(e.Sessions))
}

// Stop asks the gateway at addr to shut down and waits until it has. Without
// force it refuses while the gateway is running turns of its own. hold keeps
// it stopped: terminals will not start it again until it is started on
// purpose.
func Stop(addr string, force, hold bool) error {
	q := []string{}
	if force {
		q = append(q, "force=1")
	}
	if hold {
		q = append(q, "hold=1")
	}
	url := "http://" + addr + "/api/gateway/shutdown?" + strings.Join(q, "&")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		var b struct {
			Busy []string `json:"busy"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&b)
		return &BusyError{Sessions: b.Busy}
	}
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("stop: http %d %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(150 * time.Millisecond) {
		if !Alive(addr) {
			return nil
		}
	}
	return errors.New("the gateway said it would stop, and has not")
}

// OpenBrowser hands a web address to whatever this machine opens them with.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
