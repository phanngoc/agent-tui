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

// GatewayAddr, when set, is the gateway to talk to — handed to kit-mcp by the
// process that started the turn, since a kit-mcp started from a WSL
// distribution does not inherit its environment and might find another.
var GatewayAddr string

// gatewayAddr reads the running gateway's address from its discovery file.
func gatewayAddr() (string, error) {
	if GatewayAddr != "" {
		return GatewayAddr, nil
	}
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

// schedulingGuide tells the agent that the app schedules work itself, and to
// use that rather than the machine's own schedulers.
func schedulingGuide(native bool) string {
	tool := "the schedule tool"
	if !native {
		tool = "the schedule tool (mcp__agent-tui__schedule)"
	}
	return strings.ReplaceAll(scheduling, "TOOL", tool)
}

const scheduling = `<scheduling>
You are running inside agent-tui, which schedules work itself. For anything that should happen later or again — a reminder, polling, a periodic check or report, watching logs or a deploy — use TOOL. Do not set up cron, crontab, at, systemd timers, Windows Task Scheduler, launchd or a sleep loop for it, even inside the project, and do not use Claude Code's own /schedule skill, RemoteTrigger routines, CronCreate or /loop, which are not this app's scheduler: those run out of the user's sight, without the project's tools, memory or MCP servers, and nobody can see or stop them from agent-tui. A job made with the tool shows on the admin's Schedules page, runs in its own session (which the user can open) with the same engine, tools and MCP servers as this conversation, stays quiet when there is nothing to report, and can be paused or deleted there.
Write the job's prompt so that a fresh session can do the whole job from it alone: what to check, where, what counts as worth reporting, and to answer NO_REPLY when nothing does. A shell gate (a command that exits 0 only when there is work) saves a model call per quiet run. If a script is needed, keep it in the project and have the job's prompt run it.
</scheduling>

`

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
				"kind":         map[string]any{"type": "string", "enum": []string{"task", "heartbeat"}, "description": "create: task (default) runs prompt; heartbeat works through the project's .agent-tui/HEARTBEAT.md checklist each run (give prompt to write that checklist)."},
				"gate":         str("create: a shell command run in the project before each run; the run happens only if it exits 0, and its output is given to the agent."),
				"model":        str("create: the model for the runs; empty means the project's."),
				"mode":         map[string]any{"type": "string", "enum": []string{"plan", "ask", "auto", "full"}, "description": "create: what runs may do without asking (default auto)."},
				"hours":        str("create: only between these local hours, \"09:00-18:00\"; add days as \"09:00-18:00 mon-fri\"."),
				"timeout":      str("create: stop a run that goes on longer, e.g. 15m (default 30m)."),
				"id":           str("delete: the job's id."),
				"in":           str("next: when to check again: 5m, 1h."),
				"reason":       str("next: why then, in a few words."),
				"stop":         map[string]any{"type": "boolean", "description": "next: end this recurring job."},
			}, "action"),
		Run: func(ctx context.Context, in json.RawMessage) (string, bool) {
			var a struct {
				Action, Name, Prompt, At, Every, Cron, ID, In, Reason string
				Kind, Gate, Model, Mode, Hours, Timeout               string
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
				kind := a.Kind
				if kind == "" {
					kind = "task"
				}
				mode := a.Mode
				if mode == "" {
					mode = "auto"
				}
				job := map[string]any{"name": a.Name, "kind": kind, "root": k.Root, "prompt": a.Prompt,
					"enabled": true, "origin": "agent", "every": a.Every, "cron": a.Cron,
					"gate": a.Gate, "model": a.Model, "mode": mode, "timeout": a.Timeout}
				if a.Hours != "" {
					w, err := parseHours(a.Hours)
					if err != nil {
						return err.Error(), true
					}
					job["active_hours"] = w
				}
				if kind == "heartbeat" && strings.TrimSpace(a.Prompt) != "" {
					// The prompt is the checklist: written where the heartbeat reads it.
					if err := gatewayCall(ctx, http.MethodPut, "/api/schedules/checklist", map[string]string{"root": k.Root, "text": a.Prompt}, nil); err != nil {
						return "writing the checklist: " + err.Error(), true
					}
					job["prompt"] = ""
				}
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
				return "scheduled " + strings.TrimPrefix(j.line(), "- ") + ". It is on the admin's Schedules page; the user can run it now, pause or edit it there.", false
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

// parseHours reads "09:00-18:00" or "09:00-18:00 mon-fri" / "… sat,sun".
func parseHours(s string) (map[string]any, error) {
	f := strings.Fields(strings.ToLower(s))
	if len(f) == 0 {
		return nil, fmt.Errorf("hours %q: give \"09:00-18:00\", optionally with days", s)
	}
	a, b, ok := strings.Cut(f[0], "-")
	if !ok {
		return nil, fmt.Errorf("hours %q: give \"09:00-18:00\"", s)
	}
	w := map[string]any{"start": a, "end": b}
	if len(f) > 1 {
		names := map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}
		var days []int
		for _, part := range strings.Split(strings.Join(f[1:], ","), ",") {
			if x, y, ok := strings.Cut(part, "-"); ok {
				i, ok1 := names[x]
				j, ok2 := names[y]
				if !ok1 || !ok2 {
					return nil, fmt.Errorf("hours %q: days are mon, tue, … sun", s)
				}
				for d := i; ; d = (d + 1) % 7 {
					days = append(days, d)
					if d == j {
						break
					}
				}
				continue
			}
			d, ok := names[part]
			if !ok {
				return nil, fmt.Errorf("hours %q: days are mon, tue, … sun", s)
			}
			days = append(days, d)
		}
		w["days"] = days
	}
	return w, nil
}
