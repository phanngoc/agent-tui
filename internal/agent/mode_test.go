package agent

import "testing"

func TestModeRoundTrips(t *testing.T) {
	for _, m := range All {
		if got := ParseMode(m.String()); got != m {
			t.Errorf("ParseMode(%q) = %v, want %v", m.String(), got, m)
		}
		if m.Label() == "" || m.Detail() == "" {
			t.Errorf("mode %v has no label or detail", m)
		}
	}
}

// TestAutoIsTheDefault pins the choice: an unknown or empty mode lands on auto,
// so a session that has never been set acts without asking.
func TestAutoIsTheDefault(t *testing.T) {
	for _, in := range []string{"", "nonsense", "AUTO", " auto "} {
		if got := ParseMode(in); got != ModeAuto {
			t.Errorf("ParseMode(%q) = %v, want auto", in, got)
		}
	}
	var zero Mode
	if zero != ModeAuto {
		t.Error("the zero value must be auto, or a fresh session starts somewhere else")
	}
}

func TestModePermissions(t *testing.T) {
	for _, tc := range []struct {
		mode     Mode
		writes   bool
		confirms bool
	}{
		{ModePlan, false, false},
		{ModeAsk, true, true},
		{ModeAuto, true, false},
		{ModeFull, true, false},
	} {
		if tc.mode.Writes() != tc.writes {
			t.Errorf("%v writes = %v, want %v", tc.mode, tc.mode.Writes(), tc.writes)
		}
		if tc.mode.Confirms() != tc.confirms {
			t.Errorf("%v confirms = %v, want %v", tc.mode, tc.mode.Confirms(), tc.confirms)
		}
	}
}

func TestNextCyclesTheSafeModes(t *testing.T) {
	seen := map[Mode]bool{}
	m := Cycle[0]
	for range Cycle {
		seen[m] = true
		m = m.Next()
	}
	if len(seen) != len(Cycle) {
		t.Errorf("cycling visited %d of %d modes", len(seen), len(Cycle))
	}
	if m != Cycle[0] {
		t.Error("cycling did not wrap back to where it started")
	}
}

// TestCyclingCannotReachFull is a safety property: "no guards" removes the
// project boundary, and arriving there by tapping a key is not a decision.
func TestCyclingCannotReachFull(t *testing.T) {
	for _, start := range All {
		m := start
		for i := 0; i < len(All)*3; i++ {
			m = m.Next()
			if m == ModeFull {
				t.Fatalf("cycling from %v reached full mode after %d steps", start, i+1)
			}
		}
	}
	// It is still reachable deliberately.
	if ParseMode("full") != ModeFull {
		t.Error("full mode cannot be selected by name")
	}
	// And stepping out of it lands somewhere sane.
	if ModeFull.Next() != ModeAuto {
		t.Errorf("leaving full mode goes to %v, want auto", ModeFull.Next())
	}
}
