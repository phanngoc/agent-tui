// Package schedule runs agent work on its own: prompts and heartbeat
// checklists, on a schedule, in the gateway. See docs/lịch-tự-động.md.
//
// It takes from two designs. From OpenClaw: the heartbeat, a checklist worked
// through in one turn that stays silent when all is well (HEARTBEAT_OK); the
// schedule kinds at / every / cron; pacing, where the agent proposes its next
// check within bounds; a gate that decides without a model whether to run;
// backoff on failure. From Claude Code: /loop, a prompt repeated in the
// session it was started in, with the agent choosing the interval and able to
// stop it; a fresh session per run by default; one catch-up run for what was
// missed, not one per miss; a deterministic stagger.
package schedule

import (
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"time"
)

// Kinds of job.
const (
	KindTask      = "task"      // a prompt
	KindHeartbeat = "heartbeat" // the project's HEARTBEAT.md checklist
)

// Session styles.
const (
	SessionNew  = "new"  // a fresh session every run
	SessionSame = "same" // one session, continued run after run
)

// Job is one piece of scheduled work.
type Job struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`

	Root   string `json:"root"`
	Prompt string `json:"prompt,omitempty"`

	// The schedule: exactly one of At, Every and Cron.
	At    time.Time `json:"at,omitempty"`
	Every string    `json:"every,omitempty"` // a duration: 90s, 30m, 2h, 1d
	Cron  string    `json:"cron,omitempty"`  // 5 fields, local time
	// Pacing lets the agent choose the next run itself, within bounds.
	Pacing *Pacing `json:"pacing,omitempty"`
	// ActiveHours keeps runs inside a daily window.
	ActiveHours *Window `json:"active_hours,omitempty"`
	// Until ends a recurring job: a /loop started from a session lasts a
	// week, as Claude Code's does, so a forgotten one stops on its own.
	Until time.Time `json:"until,omitempty"`

	Session   string `json:"session,omitempty"`    // new (default) or same
	SessionID string `json:"session_id,omitempty"` // for same: the session
	Engine    string `json:"engine,omitempty"`
	Model     string `json:"model,omitempty"`
	Mode      string `json:"mode,omitempty"`
	// Gate is a shell command run before each run, in the project: a run
	// happens only when it exits 0, and what it prints is given to the agent.
	Gate string `json:"gate,omitempty"`
	// Timeout cancels a run that goes on longer: a duration, default 30m.
	Timeout string `json:"timeout,omitempty"`

	DeleteAfterRun bool `json:"delete_after_run,omitempty"`
	SkipMissed     bool `json:"skip_missed,omitempty"`
	Enabled        bool `json:"enabled"`

	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	// Origin says where the job came from: web, tui (/loop), agent, cli.
	Origin string `json:"origin,omitempty"`

	State State `json:"state"`
}

// Pacing bounds the interval the agent may choose.
type Pacing struct {
	Min string `json:"min"`
	Max string `json:"max"`
}

// Window is a daily window, local time, "09:00"–"18:00". Days limits it to
// days of the week (0 = Sunday); empty means every day. End before Start
// spans midnight.
type Window struct {
	Start string `json:"start"`
	End   string `json:"end"`
	Days  []int  `json:"days,omitempty"`
}

// State is what the scheduler keeps about a job between runs.
type State struct {
	Next       time.Time `json:"next,omitempty"`
	LastRun    time.Time `json:"last_run,omitempty"`
	LastStatus string    `json:"last_status,omitempty"`
	LastError  string    `json:"last_error,omitempty"`
	LastText   string    `json:"last_text,omitempty"`
	Failures   int       `json:"failures,omitempty"`
	Runs       int       `json:"runs,omitempty"`
	// Running is the session of the run under way, if one is.
	Running      string    `json:"running,omitempty"`
	RunStarted   time.Time `json:"run_started,omitempty"`
	RunReason    string    `json:"run_reason,omitempty"`
	Proposed     time.Time `json:"proposed,omitempty"`
	ProposedWhy  string    `json:"proposed_why,omitempty"`
	StopProposed bool      `json:"stop_proposed,omitempty"`
}

// Run statuses.
const (
	StatusOK      = "ok"      // ran and has something to say
	StatusSilent  = "silent"  // ran, and said all is well
	StatusError   = "error"   // failed
	StatusSkipped = "skipped" // did not run, for a reason given
)

// ParseDuration reads 90s, 30m, 2h, 1d, or combinations Go understands.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, errors.New("empty duration")
	}
	if strings.HasSuffix(s, "d") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64)
		if err != nil {
			return 0, fmt.Errorf("bad duration %q", s)
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("bad duration %q (try 30m, 2h, 1d)", s)
	}
	return d, nil
}

// MinInterval is the shortest a recurring job may repeat, as in Claude Code.
const MinInterval = time.Minute

// Validate checks a job before it is saved.
func (j *Job) Validate() error {
	if strings.TrimSpace(j.Name) == "" {
		return errors.New("a job needs a name")
	}
	if j.Root == "" {
		return errors.New("a job needs a project folder")
	}
	switch j.Kind {
	case KindTask:
		if strings.TrimSpace(j.Prompt) == "" {
			return errors.New("a task needs a prompt")
		}
	case KindHeartbeat:
	default:
		return fmt.Errorf("unknown kind %q (task or heartbeat)", j.Kind)
	}
	n := 0
	if !j.At.IsZero() {
		n++
	}
	if j.Every != "" {
		n++
		d, err := ParseDuration(j.Every)
		if err != nil {
			return err
		}
		if d < MinInterval {
			return fmt.Errorf("every %s is too often; the least is %s", j.Every, MinInterval)
		}
	}
	if j.Cron != "" {
		n++
		if _, err := parseCron(j.Cron); err != nil {
			return err
		}
	}
	if n == 0 && j.Pacing == nil {
		return errors.New("a job needs a schedule: at, every, cron or pacing")
	}
	if n > 1 {
		return errors.New("give one of at, every and cron")
	}
	if p := j.Pacing; p != nil {
		lo, err := ParseDuration(p.Min)
		if err != nil {
			return fmt.Errorf("pacing min: %w", err)
		}
		hi, err := ParseDuration(p.Max)
		if err != nil {
			return fmt.Errorf("pacing max: %w", err)
		}
		if lo < MinInterval || hi < lo {
			return fmt.Errorf("pacing needs %s ≤ min ≤ max", MinInterval)
		}
	}
	if w := j.ActiveHours; w != nil {
		if _, err := clock(w.Start); err != nil {
			return err
		}
		if _, err := clock(w.End); err != nil {
			return err
		}
	}
	if j.Timeout != "" {
		if _, err := ParseDuration(j.Timeout); err != nil {
			return err
		}
	}
	switch j.Session {
	case "", SessionNew, SessionSame:
	default:
		return fmt.Errorf("unknown session style %q (new or same)", j.Session)
	}
	return nil
}

func clock(s string) (int, error) {
	h, m, ok := strings.Cut(s, ":")
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || hh < 0 || hh > 24 || mm < 0 || mm > 59 {
		return 0, fmt.Errorf("bad time %q (HH:MM)", s)
	}
	return hh*60 + mm, nil
}

// inWindow says t is inside the window.
func (w *Window) inWindow(t time.Time) bool {
	if w == nil {
		return true
	}
	if len(w.Days) > 0 {
		ok := false
		for _, d := range w.Days {
			if d%7 == int(t.Weekday()) {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	a, _ := clock(w.Start)
	b, _ := clock(w.End)
	m := t.Hour()*60 + t.Minute()
	if a <= b {
		return m >= a && m < b
	}
	return m >= a || m < b
}

// fit moves t to the window's next opening, minute by minute through at most
// eight days, when it falls outside.
func (w *Window) fit(t time.Time) time.Time {
	if w == nil || w.inWindow(t) {
		return t
	}
	c := t.Truncate(time.Minute)
	for i := 0; i < 8*24*60; i++ {
		c = c.Add(time.Minute)
		if w.inWindow(c) {
			return c
		}
	}
	return t
}

// stagger is a job's fixed offset for schedules that land on :00 or :30,
// up to five minutes, from its ID: the same job always starts at the same
// moment, and many jobs at 9:00 do not all start at 9:00.
func (j *Job) stagger() time.Duration {
	h := fnv.New32a()
	_, _ = h.Write([]byte(j.ID))
	return time.Duration(h.Sum32()%300) * time.Second
}

// NextAfter is when the job next runs after t by its schedule alone, without
// pacing or backoff; false means never again.
func (j *Job) NextAfter(t time.Time) (time.Time, bool) {
	var next time.Time
	switch {
	case !j.At.IsZero():
		if !j.At.After(t) {
			return time.Time{}, false
		}
		next = j.At
	case j.Every != "":
		d, err := ParseDuration(j.Every)
		if err != nil || d <= 0 {
			return time.Time{}, false
		}
		// Anchored at creation, so a job every 6h keeps its hours.
		anchor := j.Created
		if anchor.IsZero() || anchor.After(t) {
			anchor = t
		}
		n := t.Sub(anchor)/d + 1
		next = anchor.Add(n * d)
	case j.Cron != "":
		c, err := parseCron(j.Cron)
		if err != nil {
			return time.Time{}, false
		}
		var ok bool
		if next, ok = c.next(t.Local()); !ok {
			return time.Time{}, false
		}
		if next.Minute() == 0 || next.Minute() == 30 {
			next = next.Add(j.stagger())
		}
	case j.Pacing != nil:
		d, _ := ParseDuration(j.Pacing.Min)
		next = t.Add(d)
	default:
		return time.Time{}, false
	}
	next = j.ActiveHours.fit(next)
	if !j.Until.IsZero() && next.After(j.Until) {
		return time.Time{}, false
	}
	return next, true
}

// Recurring says the job runs more than once.
func (j *Job) Recurring() bool { return j.At.IsZero() }

// Describe says the schedule in words.
func (j *Job) Describe() string {
	var s string
	switch {
	case !j.At.IsZero():
		s = "once, " + j.At.Local().Format("Mon 2 Jan 15:04")
	case j.Every != "":
		s = "every " + j.Every
	case j.Cron != "":
		s = "cron " + j.Cron
	}
	if p := j.Pacing; p != nil {
		pace := "the agent picks the interval, " + p.Min + "–" + p.Max
		if s == "" {
			s = pace
		} else {
			s += "; " + pace
		}
	}
	if w := j.ActiveHours; w != nil {
		s += ", " + w.Start + "–" + w.End
		if len(w.Days) > 0 {
			names := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
			var ds []string
			for _, d := range w.Days {
				ds = append(ds, names[d%7])
			}
			s += " " + strings.Join(ds, ",")
		}
	}
	if !j.Until.IsZero() {
		s += ", until " + j.Until.Local().Format("2 Jan 15:04")
	}
	return s
}
