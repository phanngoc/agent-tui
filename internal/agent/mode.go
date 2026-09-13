package agent

import "strings"

// Mode is how much rope the agent gets for a turn.
//
// It belongs to the session rather than the process: two conversations against
// the same project often want different answers, and switching should not mean
// restarting. Every engine maps it onto whatever its own CLI understands.
type Mode int

const (
	// ModeAuto acts without asking, inside the project. It is the default
	// because stopping to confirm every edit is what makes an agent tedious.
	ModeAuto Mode = iota
	// ModePlan investigates and proposes but changes nothing.
	ModePlan
	// ModeAsk confirms anything that would change state.
	ModeAsk
	// ModeFull removes every guard, including the project boundary.
	ModeFull
)

// Cycle is what shift+tab steps through, from most cautious to least.
//
// ModeFull is deliberately absent: reaching "no guards" by pressing a key twice
// is not a decision anyone makes on purpose. It is reachable by name, with
// /mode full or the -mode flag, where the intent is unambiguous.
var Cycle = []Mode{ModePlan, ModeAsk, ModeAuto}

// All is every mode, for the picker and for parsing.
var All = []Mode{ModePlan, ModeAsk, ModeAuto, ModeFull}

func (m Mode) String() string {
	switch m {
	case ModePlan:
		return "plan"
	case ModeAsk:
		return "ask"
	case ModeFull:
		return "full"
	default:
		return "auto"
	}
}

// Label is the name shown in the status bar.
func (m Mode) Label() string {
	switch m {
	case ModePlan:
		return "plan"
	case ModeAsk:
		return "ask first"
	case ModeFull:
		return "no guards"
	default:
		return "auto"
	}
}

// Detail explains what the mode actually permits.
func (m Mode) Detail() string {
	switch m {
	case ModePlan:
		return "reads and proposes; changes nothing"
	case ModeAsk:
		return "confirms every write and command"
	case ModeFull:
		return "no confirmation and no project boundary"
	default:
		return "edits and runs commands inside the project without asking"
	}
}

// Writes reports whether the mode allows changing anything at all.
func (m Mode) Writes() bool { return m != ModePlan }

// Confirms reports whether the user is asked before a change.
func (m Mode) Confirms() bool { return m == ModeAsk }

// Next steps to the following mode in Cycle, wrapping around. Stepping from
// outside the cycle — from ModeFull — lands back on the default.
func (m Mode) Next() Mode {
	for i, o := range Cycle {
		if o == m {
			return Cycle[(i+1)%len(Cycle)]
		}
	}
	return ModeAuto
}

// ParseMode reads a mode by name, falling back to the default.
func ParseMode(s string) Mode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "plan", "planning", "readonly", "read-only":
		return ModePlan
	case "ask", "manual", "confirm":
		return ModeAsk
	case "full", "bypass", "yolo", "dangerous":
		return ModeFull
	default:
		return ModeAuto
	}
}

// prompt is the instruction appended to the system prompt for a mode. Only the
// modes that change what the agent should *do* say anything; the rest inherit
// the base prompt unchanged.
func (m Mode) prompt() string {
	if m != ModePlan {
		return ""
	}
	return "\n\nYou are in plan mode. You have no tools that change anything: " +
		"read, search and explore, then write out what you would do and why, " +
		"concretely enough that someone could follow it. Do not say you will " +
		"make a change — you cannot. If the plan depends on something you " +
		"cannot determine, say so and ask."
}
