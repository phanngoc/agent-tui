package kit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/config"
)

// The schedule tool lets the agent schedule work itself — "remind me at 3",
// "every morning, review open PRs" — and pace a scheduled run it is in, the
// way Claude Code's CronCreate and ScheduleWakeup do. The scheduler lives in
// the gateway, so the tool talks to the gateway's API; it cannot import the
// gateway package, which builds on this one.

// gatewayAddr reads the running gateway's address from its discovery file.
func gatewayAddr() (string, error) {
	b, err := os.ReadFile(filepath.Join(config.DataDir(), "gateway.json"))
	if err == nil {
		var i struct {
			Addr string `json:"addr"`
		}
		if json.Unmarshal(b, &i) == nil && i.Addr != "" {
			c := &http.Client{Timeout: 2 * time.Second}
			if resp, err := c.Get("http://" + i.Addr + "/api/health"); err == nil {
				resp.Body.Close()
				return i.Addr, nil
			}
		}
	}
	return "", errors.New("the gateway is not running, and schedules run in it: start it with `agent-tui gateway start` (or open the web: `agent-tui web`)")
}

func gatewayCall(ctx context.Context, method, path string, body, out any) error {
	addr, err := gatewayAddr()
	if err != nil {
		return err
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, method, "http://"+addr+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
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
		return fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out != nil && len(b) > 0 {
		return json.Unmarshal(b, out)
	}
	return nil
}

type toolJob struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Prompt   string `json:"prompt"`
	Describe string `json:"describe"`
	Enabled  bool   `json:"enabled"`
	State    struct {
		Next       time.Time `json:"next"`
		LastStatus string    `json:"last_status"`
	} `json:"state"`
}

func (j toolJob) line() string {
	next := "paused"
	if j.Enabled && !j.State.Next.IsZero() {
		next = "next " + j.State.Next.Local().Format("Mon 2 Jan 15:04")
	}
	what := j.Prompt
	if j.Kind == "heartbeat" {
		what = "heartbeat checklist"
	}
	if r := []rune(what); len(r) > 80 {
		what = string(r[:80]) + "…"
	}
	return fmt.Sprintf("- %s %q: %s; %s; last %s — %s", j.ID, j.Name, j.Describe, next, orDash(j.State.LastStatus), what)
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func (k *Kit) scheduleTool() agent.Extension {
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	return agent.Extension{
		Name: "schedule",
		Def: toolDef("schedule", "Schedule work to run later on its own, in this project, through the agent-tui gateway — or pace the scheduled run you are in. "+
			"action create: a reminder (at) or recurring work (every / cron / pacing); each run is a fresh session unless same_session. "+
			"action list: this project's schedules. action delete: remove one by id. "+
			"action next: only inside a scheduled run — say when to check again (in) and why, or stop: true when the work is done for good.",
			map[string]any{
				"action":       map[string]any{"type": "string", "enum": []string{"create", "list", "delete", "next"}},
				"name":         str("create: a short name for the job."),
				"prompt":       str("create: what to do each run, written as you would ask it."),
				"at":           str("create: once, at a local time \"2026-10-05 15:00\" or after a delay \"45m\"."),
				"every":        str("create: repeat at this interval: 30m, 2h, 1d (at least 1m)."),
				"cron":         str("create: repeat by a 5-field cron expression in local time, e.g. \"3 9 * * 1-5\"."),
				"pacing_min":   str("create: let each run choose the next within min..max, e.g. 1m."),
				"pacing_max":   str("create: the upper bound for pacing, e.g. 1h."),
				"same_session": map[string]any{"type": "boolean", "description": "create: run in this conversation, as a /loop does, instead of a fresh session each time."},
				"id":           str("delete: the job's id."),
				"in":           str("next: when to check again: 5m, 1h."),
				"reason":       str("next: why then, in a few words."),
				"stop":         map[string]any{"type": "boolean", "description": "next: end this recurring job."},
			}, "action"),
		Run: func(ctx context.Context, in json.RawMessage) (string, bool) {
			var a struct {
				Action, Name, Prompt, At, Every, Cron, ID, In, Reason string
				PacingMin                                             string `json:"pacing_min"`
				PacingMax                                             string `json:"pacing_max"`
				SameSession                                           bool   `json:"same_session"`
				Stop                                                  bool
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return err.Error(), true
			}
			switch a.Action {
			case "list":
				var out struct {
					Jobs []toolJob `json:"jobs"`
				}
				if err := gatewayCall(ctx, http.MethodGet, "/api/schedules?root="+urlQuery(k.Root), nil, &out); err != nil {
					return err.Error(), true
				}
				if len(out.Jobs) == 0 {
					return "no schedules in this project", false
				}
				var b strings.Builder
				for _, j := range out.Jobs {
					b.WriteString(j.line() + "\n")
				}
				return b.String(), false
			case "delete":
				if a.ID == "" {
					return "delete needs the job's id (see action list)", true
				}
				if err := gatewayCall(ctx, http.MethodDelete, "/api/schedules/"+a.ID, nil, nil); err != nil {
					return err.Error(), true
				}
				return "deleted " + a.ID, false
			case "next":
				if k.Session == "" {
					return "this session's id is unknown, so it cannot pace a run", true
				}
				body := map[string]any{"session": k.Session, "in": a.In, "reason": a.Reason, "stop": a.Stop}
				var j toolJob
				if err := gatewayCall(ctx, http.MethodPost, "/api/schedules/next", body, &j); err != nil {
					return err.Error(), true
				}
				if a.Stop {
					return "this job stops after this run", false
				}
				return "noted: the next run is in about " + a.In + " (kept within the job's bounds)", false
			case "create":
				job := map[string]any{"name": a.Name, "kind": "task", "root": k.Root, "prompt": a.Prompt,
					"enabled": true, "origin": "agent", "every": a.Every, "cron": a.Cron}
				if a.At != "" {
					t, err := parseWhen(a.At)
					if err != nil {
						return err.Error(), true
					}
					job["at"] = t
					job["delete_after_run"] = true
				}
				if a.PacingMin != "" || a.PacingMax != "" {
					job["pacing"] = map[string]string{"min": a.PacingMin, "max": a.PacingMax}
				}
				if a.SameSession {
					if k.Session == "" {
						return "this session's id is unknown; schedule it without same_session", true
					}
					job["session"], job["session_id"] = "same", k.Session
					if a.At == "" {
						// A loop in a conversation lasts a week, as Claude Code's does.
						job["until"] = time.Now().Add(7 * 24 * time.Hour)
					}
				}
				var j toolJob
				if err := gatewayCall(ctx, http.MethodPost, "/api/schedules", job, &j); err != nil {
					return err.Error(), true
				}
				return "scheduled " + strings.TrimPrefix(j.line(), "- "), false
			}
			return "unknown action " + a.Action, true
		},
	}
}

// parseWhen reads a local time or a delay from now.
func parseWhen(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if d, err := time.ParseDuration(s); err == nil {
		return time.Now().Add(d), nil
	}
	for _, f := range []string{"2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02 15:04:05", time.RFC3339} {
		if t, err := time.ParseInLocation(f, s, time.Local); err == nil {
			return t, nil
		}
	}
	if t, err := time.ParseInLocation("15:04", s, time.Local); err == nil {
		now := time.Now()
		at := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, time.Local)
		if !at.After(now) {
			at = at.AddDate(0, 0, 1)
		}
		return at, nil
	}
	return time.Time{}, fmt.Errorf("at %q: give a local time like \"2026-10-05 15:00\" or \"15:00\", or a delay like \"45m\"", s)
}

func urlQuery(s string) string {
	r := strings.NewReplacer("%", "%25", " ", "%20", "&", "%26", "#", "%23", "+", "%2B", "?", "%3F", "\\", "%5C")
	return r.Replace(s)
}
