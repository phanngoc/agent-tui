package engine

import (
	"os"
	"path/filepath"
	"strings"
)

// Where Claude Code writes a command's output while it runs.
//
// Measured against Claude Code 2.1.287: every shell command, foreground or
// background, is written as it runs to
//
//	<temp>/claude/<project>/<session id>/tasks/<task id>.output
//
// where <project> is the working directory with every character that is not a
// letter or a digit turned into a dash. A foreground command's file is removed
// when it finishes; a background one's stays. None of this is a published
// interface, so the exact path is only the first guess: the patterns after it
// still find the file if the project is spelled differently — shortened, say,
// for a long directory — or the folder carries the user's id, as it does on
// Linux.

// claudeTaskFiles lists where a task's output may be, most likely first.
func claudeTaskFiles(cwd, sessionID, taskID string) []string {
	if sessionID == "" || taskID == "" || strings.ContainsAny(sessionID+taskID, `/\*?[`) {
		return nil
	}
	tmp := os.TempDir()
	rel := filepath.Join(sessionID, "tasks", taskID+".output")

	var out []string
	if cwd != "" {
		out = append(out, filepath.Join(tmp, "claude", claudeSlug(cwd), rel))
	}
	return append(out,
		filepath.Join(tmp, "claude", "*", rel),
		filepath.Join(tmp, "claude-*", "*", rel),
	)
}

// claudeSlug spells a directory the way Claude Code names its folder for it.
func claudeSlug(dir string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, dir)
}
