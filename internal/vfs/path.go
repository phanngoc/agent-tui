package vfs

import "strings"

// Paths here live in the filesystem's own namespace: POSIX inside a container,
// native on the host. The separator is therefore inferred from the path itself
// rather than from runtime.GOOS — a Windows host driving a Linux container has
// to get both namespaces right in the same process, and a build tag cannot.
//
// A path with neither a drive letter nor a backslash is treated exactly as it
// was before this file existed, so POSIX hosts and container paths are
// unaffected.

// driveLen is the length of a leading "C:" volume, or 0.
func driveLen(p string) int {
	if len(p) >= 2 && p[1] == ':' {
		c := p[0]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return 2
		}
	}
	return 0
}

// winStyle reports whether p is written in the Windows namespace.
func winStyle(p string) bool {
	return driveLen(p) > 0 || strings.IndexByte(p, '\\') >= 0
}

func isSep(c byte) bool { return c == '/' || c == '\\' }

// sepOf is the separator to build with for p's namespace.
func sepOf(p string) byte {
	if winStyle(p) {
		return '\\'
	}
	return '/'
}

// trimTrailingSep drops trailing separators without eating a bare volume root.
func trimTrailingSep(p string) string {
	vol := driveLen(p)
	for len(p) > vol && isSep(p[len(p)-1]) {
		p = p[:len(p)-1]
	}
	return p
}

// lastSep is the index of the final separator of either kind, or -1.
func lastSep(p string) int {
	for i := len(p) - 1; i >= 0; i-- {
		if isSep(p[i]) {
			return i
		}
	}
	return -1
}

// toSep rewrites every separator in p to s.
func toSep(p string, s byte) string {
	if strings.IndexByte(p, '/') < 0 && strings.IndexByte(p, '\\') < 0 {
		return p
	}
	b := []byte(p)
	for i := range b {
		if isSep(b[i]) {
			b[i] = s
		}
	}
	return string(b)
}

// foldPath normalises a path for comparison: separators unified, and on Windows
// case folded, because that filesystem does not distinguish it.
func foldPath(p string) string {
	q := toSep(p, '/')
	if winStyle(p) {
		return strings.ToLower(q)
	}
	return q
}

// Join concatenates path elements in the filesystem's own namespace.
func Join(base string, parts ...string) string {
	s := sepOf(base)
	out := trimTrailingSep(base)
	for _, p := range parts {
		p = strings.Trim(p, "/\\")
		if p == "" {
			continue
		}
		out += string(s) + toSep(p, s)
	}
	if out == "" {
		return string(s)
	}
	// A bare volume ("C:") is not a path; give it its root.
	if v := driveLen(out); v > 0 && len(out) == v {
		out += string(s)
	}
	return out
}

// Rel makes p relative to root, or returns p unchanged when it is outside.
//
// The result is slash-separated: it is a key for ignore rules, the fuzzy index
// and the UI, all of which speak one separator whatever the host uses.
func Rel(root, p string) string {
	fr, fp := foldPath(trimTrailingSep(root)), foldPath(p)
	switch {
	case fp == fr:
		return ""
	case strings.HasPrefix(fp, fr+"/"):
		return toSep(p[len(fr)+1:], '/')
	default:
		return p
	}
}

// Base is the final element of a path.
func Base(p string) string {
	p = trimTrailingSep(p)
	if i := lastSep(p); i >= 0 {
		return p[i+1:]
	}
	if p == "" {
		return "/"
	}
	return p
}

// Dir is everything but the final element.
func Dir(p string) string {
	q := trimTrailingSep(p)
	vol := driveLen(q)
	if i := lastSep(q); i > vol {
		return q[:i]
	}
	// One element below the root: the root itself.
	if vol > 0 {
		return q[:vol] + string(sepOf(p))
	}
	return "/"
}

// Within reports whether p resolves inside root.
//
// A relative path is relative to the root by definition, so it counts as
// inside; anything that climbs out with .. does not.
func Within(root, p string) bool {
	root = trimTrailingSep(root)
	if root == "" || p == "" {
		return false
	}
	if !isAbs(p) {
		return !strings.Contains(p, "..")
	}
	clean := foldPath(CleanPath(p))
	fr := foldPath(root)
	return clean == fr || strings.HasPrefix(clean, fr+"/")
}

// IsAbs reports whether p is rooted in its own namespace: a leading separator,
// or a drive letter followed by one.
func IsAbs(p string) bool { return isAbs(p) }

func isAbs(p string) bool {
	if v := driveLen(p); v > 0 {
		return len(p) > v && isSep(p[v])
	}
	return len(p) > 0 && isSep(p[0])
}

// CleanPath resolves . and .. without touching the filesystem, so a symlinked
// directory is compared as written rather than as resolved.
func CleanPath(p string) string {
	vol := driveLen(p)
	s := sepOf(p)
	body := p[vol:]

	parts := strings.FieldsFunc(body, func(r rune) bool { return r == '/' || r == '\\' })
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", ".":
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, part)
		}
	}
	return p[:vol] + string(s) + strings.Join(out, string(s))
}
