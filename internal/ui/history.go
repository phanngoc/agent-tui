package ui

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/phanngoc/agent-tui/internal/config"
)

// historyLimit is how many prompts are kept. A shell keeps thousands; this is
// a prompt box, and the recall is meant to reach yesterday's question, not
// last month's.
const historyLimit = 500

func historyPath() string { return filepath.Join(config.DataDir(), "history") }

// loadHistory reads the prompt history. A missing or unreadable file simply
// means there is nothing to recall.
func loadHistory() []string {
	f, err := os.Open(historyPath())
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 16<<10), 1<<20)
	for sc.Scan() {
		// Newlines are escaped on the way out so a multi-line prompt stays one
		// entry.
		line := strings.ReplaceAll(sc.Text(), `\n`, "\n")
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	if len(out) > historyLimit {
		out = out[len(out)-historyLimit:]
	}
	return out
}

// saveHistory writes the history atomically, so an interrupted write cannot
// truncate what was there before.
func (m *Model) saveHistory() {
	hist := m.history
	if len(hist) > historyLimit {
		hist = hist[len(hist)-historyLimit:]
	}

	var sb strings.Builder
	for _, line := range hist {
		sb.WriteString(strings.ReplaceAll(line, "\n", `\n`))
		sb.WriteByte('\n')
	}

	path := historyPath()
	tmp := path + ".tmp"
	if os.WriteFile(tmp, []byte(sb.String()), 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
}
