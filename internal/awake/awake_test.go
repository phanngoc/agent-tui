package awake

import (
	"runtime"
	"testing"
)

type fakeHolder struct{ holds, releases, lastDisplay int }

func (f *fakeHolder) hold(display bool) error {
	f.holds++
	f.lastDisplay = map[bool]int{true: 1, false: 0}[display]
	return nil
}
func (f *fakeHolder) release() { f.releases++ }

// The keeper holds once when wanted, again only when the screen changes, and
// lets go once when no longer wanted.
func TestKeeperHoldsAndLetsGoOnce(t *testing.T) {
	f := &fakeHolder{}
	k := &Keeper{h: f, st: Status{Supported: true}}
	k.Set(ModeBusy, true, false, "a turn is running")
	k.Set(ModeBusy, true, false, "a turn is running")
	if f.holds != 1 || !k.Status().Active || k.Status().Reason != "a turn is running" {
		t.Fatalf("holds %d status %+v", f.holds, k.Status())
	}
	k.Set(ModeBusy, true, true, "a turn is running")
	if f.holds != 2 || f.lastDisplay != 1 {
		t.Fatalf("the screen change was not held: %+v", f)
	}
	k.Set(ModeBusy, false, true, "")
	k.Set(ModeBusy, false, true, "")
	if f.releases != 1 || k.Status().Active || k.Status().Reason != "" {
		t.Fatalf("releases %d status %+v", f.releases, k.Status())
	}
	k.Set(ModeAlways, true, false, "always")
	k.Close()
	if f.releases != 2 || k.Status().Active {
		t.Fatal("Close did not let go")
	}
}

func TestResolveMode(t *testing.T) {
	if ResolveMode("") != ModeBusy || ResolveMode("bogus") != ModeBusy || ResolveMode(ModeAlways) != ModeAlways {
		t.Fatal("ResolveMode")
	}
}

// On Windows the real thing: asking and letting go both succeed.
func TestWindowsHolder(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows only")
	}
	k := New()
	if !k.Status().Supported {
		t.Fatal("SetThreadExecutionState not found")
	}
	k.Set(ModeBusy, true, true, "test")
	if st := k.Status(); !st.Active || st.Error != "" {
		t.Fatalf("hold: %+v", st)
	}
	k.Close()
}
