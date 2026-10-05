package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// cronSpec is a parsed 5-field cron expression: minute hour day-of-month
// month day-of-week, in local time. Each field is the set of values it allows.
type cronSpec struct {
	min, hour, dom, month, dow uint64
	// domAny and dowAny say the field was "*": with both day fields
	// constrained, a day matches either (vixie-cron), not both.
	domAny, dowAny bool
}

// parseCron reads the standard syntax: *, values, ranges a-b, steps */n and
// a-b/n, and comma lists. Day-of-week takes 0 or 7 for Sunday. Names (MON,
// JAN) and the extensions L, W, ? are not supported, as in Claude Code.
func parseCron(expr string) (cronSpec, error) {
	f := strings.Fields(expr)
	if len(f) != 5 {
		return cronSpec{}, fmt.Errorf("cron %q: want 5 fields (minute hour day month weekday), got %d", expr, len(f))
	}
	var c cronSpec
	var err error
	if c.min, err = cronField(f[0], 0, 59); err != nil {
		return c, fmt.Errorf("cron minute: %w", err)
	}
	if c.hour, err = cronField(f[1], 0, 23); err != nil {
		return c, fmt.Errorf("cron hour: %w", err)
	}
	if c.dom, err = cronField(f[2], 1, 31); err != nil {
		return c, fmt.Errorf("cron day of month: %w", err)
	}
	if c.month, err = cronField(f[3], 1, 12); err != nil {
		return c, fmt.Errorf("cron month: %w", err)
	}
	if c.dow, err = cronField(f[4], 0, 7); err != nil {
		return c, fmt.Errorf("cron day of week: %w", err)
	}
	if c.dow&(1<<7) != 0 {
		c.dow |= 1 // 7 is Sunday too
	}
	c.domAny, c.dowAny = f[2] == "*", f[4] == "*"
	return c, nil
}

func cronField(s string, lo, hi int) (uint64, error) {
	var set uint64
	for _, part := range strings.Split(s, ",") {
		step := 1
		if i := strings.Index(part, "/"); i >= 0 {
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n <= 0 {
				return 0, fmt.Errorf("bad step in %q", part)
			}
			step, part = n, part[:i]
		}
		a, b := lo, hi
		switch {
		case part == "*":
		case strings.Contains(part, "-"):
			x, y, _ := strings.Cut(part, "-")
			var err1, err2 error
			a, err1 = strconv.Atoi(x)
			b, err2 = strconv.Atoi(y)
			if err1 != nil || err2 != nil {
				return 0, fmt.Errorf("bad range %q", part)
			}
		default:
			n, err := strconv.Atoi(part)
			if err != nil {
				return 0, fmt.Errorf("bad value %q", part)
			}
			a, b = n, n
			if step > 1 { // "5/15" means from 5 to the end, every 15
				b = hi
			}
		}
		if a < lo || b > hi || a > b {
			return 0, fmt.Errorf("%q is outside %d-%d", part, lo, hi)
		}
		for v := a; v <= b; v += step {
			set |= 1 << uint(v)
		}
	}
	return set, nil
}

func (c cronSpec) dayMatches(t time.Time) bool {
	dom := c.dom&(1<<uint(t.Day())) != 0
	dow := c.dow&(1<<uint(t.Weekday())) != 0
	switch {
	case c.domAny && c.dowAny:
		return true
	case c.domAny:
		return dow
	case c.dowAny:
		return dom
	}
	return dom || dow
}

// next is the first minute strictly after t that the expression allows, in
// t's location. It gives up after five years, for an expression like
// "0 0 31 2 *" that never matches.
func (c cronSpec) next(t time.Time) (time.Time, bool) {
	t = t.Truncate(time.Minute).Add(time.Minute)
	end := t.AddDate(5, 0, 0)
	for t.Before(end) {
		if c.month&(1<<uint(t.Month())) == 0 {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !c.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
			continue
		}
		if c.hour&(1<<uint(t.Hour())) == 0 {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
			continue
		}
		if c.min&(1<<uint(t.Minute())) == 0 {
			t = t.Add(time.Minute)
			continue
		}
		return t, true
	}
	return time.Time{}, false
}
