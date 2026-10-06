package telegram

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/phanngoc/agent-tui/internal/config"
)

// A gateway that dies while its bot connects must not die again on every
// start.
//
// Security software can stop a program the moment it talks to Telegram's bot
// API: such programs look like malware's remote control. CrowdStrike does it
// with exit code 0xE0000027, and takes the whole gateway with it, before the
// gateway can say why. The gateway is started again by the next terminal or
// page, and would start the bot, and die, again.
//
// So the bot leaves a mark while it connects and in its first minutes; a
// clean stop, or two minutes up, takes it away. A gateway that starts and
// finds the mark of another process knows the last one died that way: it
// turns the bot off, and says so, until someone turns it on again.

const guardSpan = 2 * time.Minute

type mark struct {
	PID int       `json:"pid"`
	At  time.Time `json:"at"`
}

func guardPath() string { return filepath.Join(config.Dir(), "telegram.connecting") }

// tripped says the last gateway died with its bot connecting, and clears
// the mark.
func tripped() bool {
	b, err := os.ReadFile(guardPath())
	if err != nil {
		return false
	}
	var m mark
	_ = json.Unmarshal(b, &m)
	_ = os.Remove(guardPath())
	return m.PID != 0 && m.PID != os.Getpid()
}

func setMark() {
	b, _ := json.Marshal(mark{PID: os.Getpid(), At: time.Now()})
	_ = os.WriteFile(guardPath(), b, 0o600)
}

func clearMark() { _ = os.Remove(guardPath()) }

// PausedReason is what the admin says when the guard turned the bot off.
const PausedReason = "The gateway was stopped from outside while the bot was connecting to Telegram, so the bot was turned off. " +
	"Security software on this computer (CrowdStrike, for one) can stop programs that talk to Telegram bots: " +
	"ask IT for an exclusion for agent-tui.exe, then turn the bot on again."
