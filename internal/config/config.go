// Package config resolves runtime settings from flags, env and an optional
// JSON file at $XDG_CONFIG_HOME/agent-tui/config.json.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
)

type Config struct {
	Model       string `json:"model"`
	Effort      string `json:"effort"` // low|medium|high|xhigh|max
	MaxTokens   int64  `json:"max_tokens"`
	MaxFileKB   int    `json:"max_file_kb"` // preview + read_file guard
	IndexLimit  int    `json:"index_limit"` // max files kept in the fuzzy index
	Workers     int    `json:"workers"`     // search parallelism
	AutoApprove bool   `json:"auto_approve"`
	// Engine picks the backend that answers prompts: the built-in Anthropic
	// client, or an installed CLI.
	Engine string `json:"engine"` // api|claude|codex|opencode
	// Theme names the palette. An unknown name falls back to the default
	// rather than failing to start: a typo in a config file is not worth a
	// dead terminal.
	Theme string `json:"theme"` // herdr|monokai
	// Mode is how much a new session's agent may do: plan|ask|auto|full.
	// Auto is the default: confirming every edit is what makes an agent
	// tedious, and the mode is always on screen so it is never a surprise.
	Mode string `json:"mode"`

	Root string `json:"-"` // project root, always absolute
}

func Default() Config {
	return Config{
		Model:      "claude-opus-5",
		Effort:     "high",
		MaxTokens:  32000,
		MaxFileKB:  2048,
		IndexLimit: 200000,
		Workers:    runtime.NumCPU(),
		Mode:       "auto",
	}
}

// Dir returns the config directory, creating it on demand.
func Dir() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	d := filepath.Join(base, "agent-tui")
	_ = os.MkdirAll(d, 0o755)
	return d
}

// DataDir returns where sessions are persisted.
func DataDir() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "share")
	}
	d := filepath.Join(base, "agent-tui")
	_ = os.MkdirAll(d, 0o755)
	return d
}

// Load merges the on-disk config over the defaults. A missing or malformed
// file is not an error: the defaults simply stand.
func Load() Config {
	c := Default()
	b, err := os.ReadFile(filepath.Join(Dir(), "config.json"))
	if err == nil {
		_ = json.Unmarshal(b, &c)
	}
	if m := os.Getenv("AGENT_TUI_MODEL"); m != "" {
		c.Model = m
	}
	if c.Workers < 1 {
		c.Workers = runtime.NumCPU()
	}
	if c.MaxTokens < 1024 {
		c.MaxTokens = 32000
	}
	return c
}
