// Package ignore matches paths against a project's .gitignore plus a built-in
// list of directories and extensions that are never worth indexing.
//
// It is its own package because both the local and the container filesystems
// need it, and it must not depend on either.
package ignore

import (
	"bufio"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// defaultDirs are skipped before any .gitignore is consulted. Keeping them in a
// set means the walker never descends into the directories that dominate the
// file count of a typical repo.
var defaultDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, ".jj": true,
	"node_modules": true, "vendor": true, "target": true,
	".venv": true, "venv": true, "__pycache__": true, ".mypy_cache": true,
	".pytest_cache": true, ".ruff_cache": true, ".tox": true,
	"dist": true, "build": true, "out": true, ".next": true, ".nuxt": true,
	".cache": true, ".parcel-cache": true, ".turbo": true, ".gradle": true,
	".idea": true, ".vscode": true, ".DS_Store": true, "Pods": true,
	".terraform": true, "bower_components": true, ".bundle": true,
}

// defaultExts are binary-ish files that are never worth indexing.
var defaultExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".bmp": true,
	".ico": true, ".webp": true, ".tiff": true, ".psd": true, ".svgz": true,
	".pdf": true, ".zip": true, ".gz": true, ".bz2": true, ".xz": true,
	".tar": true, ".7z": true, ".rar": true, ".jar": true, ".war": true,
	".exe": true, ".dll": true, ".so": true, ".dylib": true, ".a": true,
	".o": true, ".obj": true, ".class": true, ".pyc": true, ".pyo": true,
	".wasm": true, ".bin": true, ".dat": true, ".db": true, ".sqlite": true,
	".mp3": true, ".mp4": true, ".mov": true, ".avi": true, ".mkv": true,
	".wav": true, ".flac": true, ".ogg": true, ".webm": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
}

type rule struct {
	pat     string
	dirOnly bool
	negate  bool
	rooted  bool
}

// Set matches paths against a project's ignore rules.
type Set struct {
	rules []rule
}

// Load reads .gitignore and friends from root. Missing files are fine; the
// built-in defaults always apply.
func Load(root string) *Set {
	ig := &Set{}
	for _, name := range []string{".gitignore", ".ignore", filepath.Join(".git", "info", "exclude")} {
		ig.loadFile(filepath.Join(root, name))
	}
	return ig
}

func (ig *Set) loadFile(p string) {
	f, err := os.Open(p)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	ig.loadLines(lines)
}

// loadLines parses gitignore syntax: comments, negation, directory-only rules
// and rooted patterns.
func (ig *Set) loadLines(lines []string) {
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r := rule{pat: line}
		if strings.HasPrefix(r.pat, "!") {
			r.negate, r.pat = true, r.pat[1:]
		}
		if strings.HasSuffix(r.pat, "/") {
			r.dirOnly, r.pat = true, strings.TrimSuffix(r.pat, "/")
		}
		if strings.HasPrefix(r.pat, "/") {
			r.rooted, r.pat = true, strings.TrimPrefix(r.pat, "/")
		} else if strings.Contains(r.pat, "/") {
			r.rooted = true
		}
		if r.pat == "" {
			continue
		}
		ig.rules = append(ig.rules, r)
	}
}

// Match reports whether rel (a slash-separated path relative to the root)
// should be skipped. Later rules win, which is how gitignore negation works.
func (ig *Set) Match(rel string, isDir bool) bool {
	base := path.Base(rel)
	if isDir && defaultDirs[base] {
		return true
	}
	if !isDir {
		if defaultExts[strings.ToLower(path.Ext(base))] {
			return true
		}
		if base == ".DS_Store" {
			return true
		}
	}

	skip := false
	for i := range ig.rules {
		r := &ig.rules[i]
		if r.dirOnly && !isDir {
			continue
		}
		target := base
		if r.rooted {
			target = rel
		}
		ok, err := path.Match(r.pat, target)
		if err != nil {
			continue
		}
		// A rooted directory rule also covers everything beneath it.
		if !ok && r.rooted && strings.HasPrefix(rel, r.pat+"/") {
			ok = true
		}
		if ok {
			skip = !r.negate
		}
	}
	return skip
}

// PruneDirs returns the directory names that are skipped unconditionally.
// The container filesystem passes these to `find -prune`, which keeps the
// remote walk from descending into them in the first place.
func PruneDirs() []string {
	out := make([]string, 0, len(defaultDirs))
	for name := range defaultDirs {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Empty is a matcher with only the built-in defaults.
func Empty() *Set { return &Set{} }

// Parse builds a matcher from gitignore text, for filesystems where the file
// cannot simply be opened.
func Parse(text string) *Set {
	ig := &Set{}
	ig.loadLines(strings.Split(text, "\n"))
	return ig
}
