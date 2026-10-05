package kit

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// A project's turns are told the app schedules work itself — by the tool's
// MCP name for a CLI engine — so "every hour" becomes a schedule, not a
// crontab entry.
func TestTurnsAreToldToScheduleThroughTheApp(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	root := t.TempDir()
	for engine, name := range map[string]string{"api": "the schedule tool.", "claude": "mcp__agent-tui__schedule"} {
		x, _ := For(root).Extras(context.Background(), engine, "check the logs every hour")
		if !strings.Contains(x.System, "<scheduling>") || !strings.Contains(x.System, name) || !strings.Contains(x.System, "crontab") {
			t.Errorf("%s: the system prompt does not steer scheduling to the app:\n%s", engine, x.System)
		}
	}
}

func TestParseHours(t *testing.T) {
	w, err := parseHours("09:00-18:00 mon-fri")
	if err != nil || w["start"] != "09:00" || w["end"] != "18:00" || !reflect.DeepEqual(w["days"], []int{1, 2, 3, 4, 5}) {
		t.Fatalf("parseHours = %v, %v", w, err)
	}
	w, _ = parseHours("22:00-06:00 sat,sun")
	if !reflect.DeepEqual(w["days"], []int{6, 0}) {
		t.Fatalf("days = %v", w["days"])
	}
	if _, err := parseHours("nine to five"); err == nil {
		t.Fatal("nonsense parsed")
	}
}
