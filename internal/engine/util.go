package engine

import (
	"encoding/json"
	"strings"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/config"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func joinText(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + "\n\n" + b
	}
}

// flattenContent renders a tool_result payload, which the Claude CLI sends
// either as a bare string or as a list of content blocks.
func flattenContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var sb strings.Builder
		for _, b := range blocks {
			if b.Text != "" {
				if sb.Len() > 0 {
					sb.WriteByte('\n')
				}
				sb.WriteString(b.Text)
			}
		}
		if sb.Len() > 0 {
			return sb.String()
		}
	}
	return string(raw)
}

// addDirs are the turn's folders besides the project for a CLI's --add-dir:
// those that exist, since the CLIs refuse one that does not, and only for a
// CLI on this machine, where they are spelled as this machine spells them.
func addDirs(t agent.Turn) []string {
	if t.FS != nil && !t.FS.IsLocal() {
		return nil
	}
	var out []string
	for _, d := range config.ExistingDirs(t.Dirs) {
		if t.Root == "" || !vfs.Within(t.Root, d) {
			out = append(out, d)
		}
	}
	return out
}
