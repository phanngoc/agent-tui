// Package awake keeps the computer from sleeping while the agent works:
// caffeinate, for agent-tui.
//
// An agent left on a long task, or a schedule that runs every hour, is no
// use on a laptop that goes to sleep after fifteen idle minutes. Each system
// has its own way to say "not now", and the tools that do it — macOS's
// caffeinate, PowerToys Awake, systemd-inhibit — are thin wrappers over it:
//
//   - Windows: SetThreadExecutionState(ES_CONTINUOUS | ES_SYSTEM_REQUIRED,
//     and ES_DISPLAY_REQUIRED for the screen), held by one OS thread for as
//     long as it lasts (awake_windows.go);
//   - macOS: caffeinate -i (-d for the screen) -w <this process>;
//   - Linux: systemd-inhibit --what=idle:sleep, around a sleep that is
//     killed to let go.
//
// None of them stops a sleep someone asks for — closing the lid, the power
// button, the Start menu: they stop the idle timer, which is what puts an
// unattended machine to sleep.
package awake

import (
	"sync"
	"time"
)

// Modes, set in the admin (prefs.json: keep_awake).
const (
	ModeOff       = "off"
	ModeBusy      = "busy"      // while a turn runs, anywhere (the default)
	ModeSchedules = "schedules" // also while any scheduled job is on
	ModeAlways    = "always"    // while the gateway runs
)

// ResolveMode spells out the default.
func ResolveMode(m string) string {
	switch m {
	case ModeOff, ModeBusy, ModeSchedules, ModeAlways:
		return m
	}
	return ModeBusy
}

// holder is what each system provides: hold the machine awake (and the
// screen with it), or let go.
type holder interface {
	hold(display bool) error
	release()
}

// Status is what the admin shows.
type Status struct {
	Mode      string    `json:"mode"`
	Display   bool      `json:"display"`
	Active    bool      `json:"active"`
	Reason    string    `json:"reason,omitempty"`
	Since     time.Time `json:"since,omitzero"`
	Supported bool      `json:"supported"`
	Method    string    `json:"method"`
	Error     string    `json:"error,omitempty"`
}

// Keeper holds the machine awake when told it is wanted, and lets go when
// it is not; telling it the same twice does nothing.
type Keeper struct {
	mu sync.Mutex
	h  holder
	st Status
	// held is what is held now: the screen too, or not.
	held, heldDisplay bool
}

// New is a keeper for this system.
func New() *Keeper {
	h, method := newHolder()
	return &Keeper{h: h, st: Status{Supported: h != nil, Method: method}}
}

// Set says whether the machine should stay awake now, and why.
func (k *Keeper) Set(mode string, want, display bool, reason string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.st.Mode, k.st.Display = mode, display
	if k.h == nil {
		k.st.Active, k.st.Reason = false, ""
		return
	}
	switch {
	case want && (!k.held || k.heldDisplay != display):
		if err := k.h.hold(display); err != nil {
			k.st.Error, k.st.Active = err.Error(), false
			return
		}
		if !k.held {
			k.st.Since = time.Now()
		}
		k.held, k.heldDisplay = true, display
		k.st.Active, k.st.Error = true, ""
	case !want && k.held:
		k.h.release()
		k.held = false
		k.st.Active, k.st.Since = false, time.Time{}
	}
	if want {
		k.st.Reason = reason
	} else {
		k.st.Reason = ""
	}
}

// Close lets go, for good.
func (k *Keeper) Close() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.h != nil && k.held {
		k.h.release()
		k.held = false
	}
	k.st.Active = false
}

// Status is the keeper's state now.
func (k *Keeper) Status() Status {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.st
}
