package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Folders beyond the project that an agent in auto mode may change without
// asking.
//
// Auto promises to act inside the project and ask about the rest. Taken
// literally that stops a turn every time an agent does what agents routinely
// do outside it: clone a second repository into /tmp to make a PR there, write
// a PR body or a scratch script to the temp folder, work in a worktree of this
// very project. None of that is the boundary the mode is protecting, and an
// approval nobody reads before saying yes protects nothing.
//
// So some folders are on the project's side of the line:
//
//   - the temp folders, which are scratch by definition and which every tool
//     — Claude Code's own scratchpad among them — already writes to freely;
//   - <project>.worktrees, where agent-tui puts this project's worktrees;
//   - and whatever the user adds: allowed_dirs in config.json for every
//     project, or in a project's .agent-tui/settings.json for that one.
//
// The list widens auto only. Ask still confirms every change, plan still
// changes nothing, and a read was never asked about in the first place.

// AllowedDirs are the folders an agent working on root may change in auto
// mode besides root itself, absolute and without duplicates. A folder that
// does not exist yet is kept: /tmp/x may be about to be made.
func AllowedDirs(root string) []string {
	var dirs []string
	dirs = append(dirs, tempDirs()...)
	if root != "" {
		root = filepath.Clean(root)
		dirs = append(dirs, filepath.Join(filepath.Dir(root), filepath.Base(root)+".worktrees"))
	}
	dirs = append(dirs, Load().AllowedDirs...)
	if root != "" {
		dirs = append(dirs, LoadProjectSettings(root).AllowedDirs...)
	}
	return cleanDirs(dirs)
}

// tempDirs are this machine's scratch folders, by every name an agent might
// use for them. On Windows that includes /tmp: Claude Code runs its commands
// in Git Bash, where /tmp is the temp folder, and its file tools are then
// handed /tmp paths too.
func tempDirs() []string {
	dirs := []string{os.TempDir()}
	for _, v := range []string{"TMPDIR", "TEMP", "TMP"} {
		dirs = append(dirs, os.Getenv(v))
	}
	if runtime.GOOS == "windows" {
		// TEMP is often the 8.3 short form (C:\Users\PHAN~1.NGO\…), which
		// is not how a path the agent writes is spelled.
		if l := os.Getenv("LOCALAPPDATA"); l != "" {
			dirs = append(dirs, filepath.Join(l, "Temp"))
		}
		return append(dirs, "/tmp")
	}
	return append(dirs, "/tmp", "/var/tmp")
}

// cleanDirs expands ~ and environment variables, makes each path absolute and
// clean, follows a symlink so /tmp and /private/tmp are one folder on a Mac,
// and drops what is empty, relative or already listed.
func cleanDirs(in []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(d string) {
		key := d
		if runtime.GOOS == "windows" {
			key = strings.ToLower(d)
		}
		if !seen[key] {
			seen[key] = true
			out = append(out, d)
		}
	}
	for _, d := range in {
		d = strings.TrimSpace(os.ExpandEnv(d))
		if d == "~" || strings.HasPrefix(d, "~/") || strings.HasPrefix(d, `~\`) {
			if home, err := os.UserHomeDir(); err == nil {
				d = filepath.Join(home, d[1:])
			}
		}
		// /tmp is absolute to an agent on Windows even though it is not to
		// filepath there: keep it as it is written.
		if strings.HasPrefix(d, "/") && runtime.GOOS == "windows" {
			if d = strings.TrimRight(d, "/"); d != "" {
				add(d)
			}
			continue
		}
		if d == "" || !filepath.IsAbs(d) {
			continue
		}
		d = filepath.Clean(d)
		add(d)
		if real, err := filepath.EvalSymlinks(d); err == nil && real != d {
			add(real)
		}
	}
	return out
}

// ExistingDirs keeps the folders that are there now, as this machine spells
// them: what a CLI's --add-dir accepts, since it refuses one that is not.
func ExistingDirs(dirs []string) []string {
	var out []string
	for _, d := range dirs {
		if !filepath.IsAbs(d) {
			continue
		}
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			out = append(out, d)
		}
	}
	return out
}
