package ui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/phanngoc/agent-tui/internal/gateway"
)

// /loop repeats a prompt in this session, as Claude Code's does: with an
// interval, on that schedule; without one, at an interval the agent picks
// each time, between a minute and an hour, until it says the work is done.
// The loop is a job in the gateway's scheduler, so it runs whether or not this
// terminal is open — and when it is, the runs appear here, since the gateway
// routes them to the terminal holding the session. A loop lasts a week.

// scheduleMsg reports what /loop or /schedule did.
type scheduleMsg struct{ text string }

// loopPrompt is what a bare /loop runs: .agent-tui/loop.md if the project
// has one, else upkeep of the work already under way.
const loopPrompt = "Continue any unfinished work from this conversation. Then tend to the current branch: failing checks, review comments, merge conflicts. " +
	"If nothing is pending, say so in one line. Start nothing new beyond that, and take no irreversible step (push, delete) that the conversation has not already asked for."

var (
	leadInterval  = regexp.MustCompile(`^(\d+)\s*(s|m|h|d)\b\s*`)
	trailInterval = regexp.MustCompile(`(?i)\s*\bevery\s+(\d+)\s*(s|sec|secs|seconds?|m|min|mins|minutes?|h|hr|hrs|hours?|d|days?)\s*$`)
)

// parseLoop splits "/loop 5m check the deploy" or "/loop check the deploy
// every 2 hours" into an interval and a prompt. Seconds round up to a
// minute, the least a schedule repeats.
func parseLoop(arg string) (every, prompt string) {
	arg = strings.TrimSpace(arg)
	unit := func(n, u string) string {
		v, _ := strconv.Atoi(n)
		switch strings.ToLower(u[:1]) {
		case "s":
			return strconv.Itoa(max(1, (v+59)/60)) + "m"
		case "m":
			return strconv.Itoa(max(1, v)) + "m"
		case "h":
			return strconv.Itoa(v) + "h"
		}
		return strconv.Itoa(v) + "d"
	}
	if m := leadInterval.FindStringSubmatch(arg); m != nil {
		return unit(m[1], m[2]), strings.TrimSpace(arg[len(m[0]):])
	}
	if m := trailInterval.FindStringSubmatch(arg); m != nil {
		return unit(m[1], m[2]), strings.TrimSpace(arg[:len(arg)-len(m[0])])
	}
	return "", arg
}

// gatewayDo calls the gateway's API, starting the gateway if it is not
// running: scheduling something is asking for it.
func gatewayDo(method, path string, body, out any) error {
	addr, _, err := gateway.Ensure()
	if err != nil {
		return err
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, "http://"+addr+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c := &http.Client{Timeout: 20 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	if out != nil && len(b) > 0 {
		return json.Unmarshal(b, out)
	}
	return nil
}

type loopJob struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	SessionID string `json:"session_id"`
	Origin    string `json:"origin"`
	Describe  string `json:"describe"`
	Enabled   bool   `json:"enabled"`
	State     struct {
		Next       time.Time `json:"next"`
		LastStatus string    `json:"last_status"`
	} `json:"state"`
}

// startLoop makes this session's loop.
func (m *Model) startLoop(arg string) tea.Cmd {
	s := m.mgr.Active()
	if s == nil {
		return nil
	}
	if strings.TrimSpace(arg) == "stop" {
		return m.stopLoops()
	}
	every, prompt := parseLoop(arg)
	if prompt == "" {
		prompt = loopPrompt
		if b, err := os.ReadFile(filepath.Join(s.Root, ".agent-tui", "loop.md")); err == nil && strings.TrimSpace(string(b)) != "" {
			prompt = strings.TrimSpace(string(b))
		}
	}
	m.mgr.SaveNow(s)
	name := []rune(strings.Join(strings.Fields(prompt), " "))
	if len(name) > 40 {
		name = append(name[:40], '…')
	}
	job := map[string]any{"name": "loop: " + string(name), "kind": "task", "root": s.Root, "prompt": prompt,
		"session": "same", "session_id": s.ID, "until": time.Now().Add(7 * 24 * time.Hour),
		"engine": s.Engine, "model": s.Model, "mode": s.Mode, "enabled": true, "origin": "tui"}
	if every != "" {
		job["every"] = every
	} else {
		job["pacing"] = map[string]string{"min": "1m", "max": "1h"}
	}
	m.notice = "starting the loop…"
	return func() tea.Msg {
		var j loopJob
		if err := gatewayDo(http.MethodPost, "/api/schedules", job, &j); err != nil {
			return scheduleMsg{"loop: " + err.Error()}
		}
		// The first run now, as a loop starts with the work, then repeats it.
		_ = gatewayDo(http.MethodPost, "/api/schedules/"+j.ID+"/run", nil, nil)
		how := "every " + every
		if every == "" {
			how = "at an interval the agent picks (1m–1h)"
		}
		return scheduleMsg{"loop " + j.ID + " started: now, then " + how + " for a week · /loop stop ends it"}
	}
}

func (m *Model) stopLoops() tea.Cmd {
	s := m.mgr.Active()
	return func() tea.Msg {
		var out struct {
			Jobs []loopJob `json:"jobs"`
		}
		if err := gatewayDo(http.MethodGet, "/api/schedules?root="+url.QueryEscape(s.Root), nil, &out); err != nil {
			return scheduleMsg{"loop: " + err.Error()}
		}
		n := 0
		for _, j := range out.Jobs {
			if j.SessionID == s.ID {
				if gatewayDo(http.MethodDelete, "/api/schedules/"+j.ID, nil, nil) == nil {
					n++
				}
			}
		}
		if n == 0 {
			return scheduleMsg{"no loop in this session"}
		}
		return scheduleMsg{fmt.Sprintf("stopped %d loop(s) in this session", n)}
	}
}

// listSchedules says what is scheduled in this project, in a line; /schedule
// web opens the full page.
func (m *Model) listSchedules(arg string) tea.Cmd {
	s := m.mgr.Active()
	if s == nil {
		return nil
	}
	if strings.TrimSpace(arg) == "web" {
		root := s.Root
		return func() tea.Msg {
			addr, _, err := gateway.Ensure()
			if err != nil {
				return scheduleMsg{"schedule: " + err.Error()}
			}
			u := "http://" + addr + "/schedules?root=" + url.QueryEscape(root)
			_ = gateway.OpenBrowser(u)
			return scheduleMsg{"opened " + u}
		}
	}
	return func() tea.Msg {
		var out struct {
			Jobs []loopJob `json:"jobs"`
		}
		if err := gatewayDo(http.MethodGet, "/api/schedules?root="+url.QueryEscape(s.Root), nil, &out); err != nil {
			return scheduleMsg{"schedule: " + err.Error()}
		}
		if len(out.Jobs) == 0 {
			return scheduleMsg{"nothing scheduled here · /loop <prompt> starts one · /schedule web for the page"}
		}
		var parts []string
		for _, j := range out.Jobs {
			when := "paused"
			if j.Enabled && !j.State.Next.IsZero() {
				when = j.State.Next.Local().Format("15:04")
				if j.State.Next.Sub(time.Now()) > 20*time.Hour {
					when = j.State.Next.Local().Format("Mon 15:04")
				}
			}
			mark := ""
			if j.SessionID == s.ID {
				mark = "*"
			}
			parts = append(parts, fmt.Sprintf("%s%s (%s)", mark, j.Name, when))
		}
		return scheduleMsg{fmt.Sprintf("%d scheduled: %s · /schedule web", len(out.Jobs), strings.Join(parts, " · "))}
	}
}
