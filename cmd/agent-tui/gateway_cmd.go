package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/phanngoc/agent-tui/internal/config"
	"github.com/phanngoc/agent-tui/internal/gateway"
)

// The gateway on its own. The terminal app starts one when it finds none, but
// it is not the terminal's: it runs in the background until stopped, serves
// the web whether or not a terminal is open, and these commands start, stop
// and look at it from anywhere — a shell, the Start menu, a script.

const gatewayUsage = `usage: agent-tui gateway [command]

  status            is it running, where, which version, what it is doing (default)
  start             start it in the background, if it is not running
  stop [-force]     stop it; terminals will not start it again until it is started
                    on purpose. -force cancels turns it is running.
  restart [-force]  stop it and start it again, e.g. after updating agent-tui
  open [path]       start it if needed and open the web admin (same as: agent-tui web)
  log [-n N]        the last N lines of its log (default 40)
`

func gatewayCmd(args []string) error {
	sub := "status"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("gateway "+sub, flag.ContinueOnError)
	force := fs.Bool("force", false, "cancel turns the gateway is running")
	lines := fs.Int("n", 40, "lines of log")
	fs.Usage = func() { fmt.Fprint(os.Stderr, gatewayUsage) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	switch sub {
	case "status":
		return gatewayStatus()
	case "start":
		addr, started, err := gateway.Ensure()
		if err != nil {
			return err
		}
		if started {
			fmt.Printf("gateway started on http://%s\n", addr)
		} else {
			fmt.Printf("gateway already running on http://%s\n", addr)
		}
		return nil
	case "stop":
		addr, ok := gateway.Find()
		if !ok {
			fmt.Println("gateway is not running")
			return nil
		}
		if err := gateway.Stop(addr, *force, true); err != nil {
			return err
		}
		fmt.Println("gateway stopped; start it again with: agent-tui gateway start")
		return nil
	case "restart":
		if addr, ok := gateway.Find(); ok {
			if err := gateway.Stop(addr, *force, false); err != nil {
				return err
			}
		}
		addr, _, err := gateway.Ensure()
		if err != nil {
			return err
		}
		fmt.Printf("gateway restarted on http://%s\n", addr)
		return nil
	case "open":
		return openWeb(fs.Arg(0))
	case "log":
		return gatewayLog(*lines)
	case "help", "-h", "--help":
		fmt.Print(gatewayUsage)
		return nil
	}
	fmt.Fprint(os.Stderr, gatewayUsage)
	return fmt.Errorf("unknown gateway command %q", sub)
}

// openWeb starts the gateway if it is not running and opens the admin, at
// path when one is given (sessions, memory, …).
func openWeb(path string) error {
	addr, started, err := gateway.Ensure()
	if err != nil {
		return err
	}
	url := "http://" + addr + "/" + strings.TrimPrefix(path, "/")
	if started {
		fmt.Printf("gateway started on http://%s\n", addr)
	}
	fmt.Println("opening " + url)
	return gateway.OpenBrowser(url)
}

func gatewayStatus() error {
	addr, ok := gateway.Find()
	if !ok {
		fmt.Println("gateway: not running")
		if gateway.StoppedOnPurpose() {
			fmt.Println("  stopped on purpose: terminals will not start it until you do")
		}
		fmt.Println("  start it with: agent-tui gateway start   (or open the web: agent-tui web)")
		return nil
	}
	var h struct {
		Version string    `json:"version"`
		PID     int       `json:"pid"`
		Started time.Time `json:"started"`
		Peers   int       `json:"peers"`
		Running []string  `json:"running"`
	}
	c := &http.Client{Timeout: 3 * time.Second}
	if resp, err := c.Get("http://" + addr + "/api/health"); err == nil {
		_ = json.NewDecoder(resp.Body).Decode(&h)
		resp.Body.Close()
	}
	fmt.Printf("gateway: running on http://%s\n", addr)
	fmt.Printf("  pid %d, up %s, version %s\n", h.PID, time.Since(h.Started).Round(time.Second), h.Version)
	fmt.Printf("  %d terminal(s) connected, %d turn(s) running in the gateway\n", h.Peers, len(h.Running))
	if v := version(); h.Version != "" && v != "dev" && h.Version != v {
		fmt.Printf("  this agent-tui is %s; run `agent-tui gateway restart` to update the gateway\n", v)
	}
	fmt.Println("  log: " + filepath.Join(config.DataDir(), "gateway.log"))
	return nil
}

func gatewayLog(n int) error {
	b, err := os.ReadFile(filepath.Join(config.DataDir(), "gateway.log"))
	if err != nil {
		return err
	}
	ls := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if n > 0 && len(ls) > n {
		ls = ls[len(ls)-n:]
	}
	fmt.Println(strings.Join(ls, "\n"))
	return nil
}
