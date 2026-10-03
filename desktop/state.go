package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// What the window remembers: its size, whether it was maximised, and the
// font size. It lives beside agent-tui's own layout.json, in the same data
// directory, because it is the same kind of thing — how you like it set out.

const defaultFontSize = 13

type state struct {
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	Maximized bool    `json:"maximized"`
	FontSize  float32 `json:"font_size"`
}

func statePath() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "agent-tui", "desktop.json")
}

// loadState reads the saved state, filling in what is missing or unusable.
func loadState() *state {
	st := &state{}
	if b, err := os.ReadFile(statePath()); err == nil {
		_ = json.Unmarshal(b, st)
	}
	if st.Width < 640 || st.Height < 400 {
		st.Width, st.Height = 1200, 760
		if w, h, ok := firstRunSize(); ok {
			st.Width, st.Height = max(640, w), max(400, h)
		}
	}
	if st.FontSize < 8 || st.FontSize > 36 {
		st.FontSize = defaultFontSize
	}
	return st
}

// save writes the state atomically; a crash halfway leaves the old file.
func (st *state) save() {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	p := statePath()
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	tmp := p + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, p)
	}
}
