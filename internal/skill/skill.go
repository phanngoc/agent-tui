// Package skill stores the agent's skills: named, reusable instructions the
// agent loads when a task calls for one.
//
// A skill is a folder holding a SKILL.md — YAML-ish frontmatter with a name and
// a description, then a Markdown body — which is the layout Claude Code uses,
// so a skill written for one works in the other and can be copied across.
//
// Skills live in two places. Global ones, in the config directory, are there
// in every project; project ones, in the project's .agent-tui folder, travel
// with the repository. A project skill with the same name as a global one
// replaces it in that project.
package skill

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/phanngoc/agent-tui/internal/config"
)

// Scope says where a skill is kept.
type Scope string

const (
	Global  Scope = "global"
	Project Scope = "project"
)

// Skill is one SKILL.md.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
	Scope       Scope  `json:"scope"`
	// Path is the SKILL.md file.
	Path string `json:"path"`
	// Files are the other files in the skill's folder, relative to it:
	// scripts and references a body may point at.
	Files []string `json:"files,omitempty"`
	// Disabled is set when the project switched this skill off.
	Disabled bool `json:"disabled,omitempty"`
	// Shadowed is set on a global skill a project skill of the same name
	// replaces.
	Shadowed bool `json:"shadowed,omitempty"`
	// Learned marks a skill the automatic memory proposed from experience.
	Learned bool      `json:"learned,omitempty"`
	Updated time.Time `json:"updated"`
}

// Store reads and writes the skills of one project plus the global ones.
type Store struct {
	GlobalDir  string
	ProjectDir string // empty when there is no project
}

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ValidName reports whether name can be a skill's folder name.
func ValidName(name string) bool { return nameRE.MatchString(name) }

func (s Store) dir(scope Scope) (string, error) {
	switch scope {
	case Global:
		return s.GlobalDir, nil
	case Project:
		if s.ProjectDir == "" {
			return "", errors.New("no project is open")
		}
		return s.ProjectDir, nil
	}
	return "", fmt.Errorf("unknown scope %q", scope)
}

// List returns every skill from both scopes, project ones first, sorted by
// name within each. Global skills a project skill replaces are marked
// Shadowed rather than dropped, so a page listing them can say why.
func (s Store) List() []Skill {
	var out []Skill
	seen := map[string]bool{}
	if s.ProjectDir != "" {
		for _, sk := range readDir(s.ProjectDir, Project) {
			seen[sk.Name] = true
			out = append(out, sk)
		}
	}
	for _, sk := range readDir(s.GlobalDir, Global) {
		sk.Shadowed = seen[sk.Name]
		out = append(out, sk)
	}
	return out
}

// Active is what the agent may use: List without the shadowed and the
// switched-off ones.
func (s Store) Active(disabled []string) []Skill {
	var out []Skill
	for _, sk := range s.List() {
		if sk.Shadowed || contains(disabled, sk.Name) {
			continue
		}
		out = append(out, sk)
	}
	return out
}

// Get finds a skill by name, the project's first.
func (s Store) Get(name string) (Skill, bool) {
	for _, sk := range s.List() {
		if sk.Name == name && !sk.Shadowed {
			return sk, true
		}
	}
	return Skill{}, false
}

// Save writes a skill to its scope, creating or replacing it. A rename is a
// save under the new name followed by Delete of the old one.
func (s Store) Save(sk Skill) (Skill, error) {
	if !ValidName(sk.Name) {
		return sk, fmt.Errorf("%q is not a valid skill name: use lowercase letters, digits, '.', '_' and '-'", sk.Name)
	}
	if strings.TrimSpace(sk.Description) == "" {
		return sk, errors.New("a skill needs a description: it is how the agent decides to use it")
	}
	base, err := s.dir(sk.Scope)
	if err != nil {
		return sk, err
	}
	dir := filepath.Join(base, sk.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return sk, err
	}
	sk.Path = filepath.Join(dir, "SKILL.md")
	tmp := sk.Path + ".tmp"
	if err := os.WriteFile(tmp, []byte(Render(sk)), 0o644); err != nil {
		return sk, err
	}
	if err := os.Rename(tmp, sk.Path); err != nil {
		return sk, err
	}
	got, err := Read(sk.Path, sk.Scope)
	if err != nil {
		return sk, err
	}
	return got, nil
}

// Delete removes a skill's folder from one scope.
func (s Store) Delete(scope Scope, name string) error {
	if !ValidName(name) {
		return fmt.Errorf("%q is not a skill name", name)
	}
	base, err := s.dir(scope)
	if err != nil {
		return err
	}
	dir := filepath.Join(base, name)
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		return fmt.Errorf("no %s skill named %q", scope, name)
	}
	return os.RemoveAll(dir)
}

// Copy copies a skill folder from anywhere (another tool's skills, another
// scope) into scope, files and all.
func (s Store) Copy(src string, scope Scope, name string) (Skill, error) {
	base, err := s.dir(scope)
	if err != nil {
		return Skill{}, err
	}
	if !ValidName(name) {
		return Skill{}, fmt.Errorf("%q is not a valid skill name", name)
	}
	dst := filepath.Join(base, name)
	err = filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		return Skill{}, err
	}
	return Read(filepath.Join(dst, "SKILL.md"), scope)
}

func readDir(dir string, scope Scope) []Skill {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sk, err := Read(filepath.Join(dir, e.Name(), "SKILL.md"), scope)
		if err != nil {
			continue
		}
		out = append(out, sk)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Read parses one SKILL.md. A skill whose frontmatter names nobody takes the
// name of its folder.
func Read(path string, scope Scope) (Skill, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}
	st, _ := os.Stat(path)
	meta, body := Parse(string(b))
	sk := Skill{
		Name:        meta["name"],
		Description: meta["description"],
		Body:        body,
		Scope:       scope,
		Path:        path,
		Learned:     meta["learned"] == "true",
	}
	if st != nil {
		sk.Updated = st.ModTime()
	}
	dir := filepath.Dir(path)
	if sk.Name == "" {
		sk.Name = filepath.Base(dir)
	}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || p == path {
			return nil
		}
		if rel, rerr := filepath.Rel(dir, p); rerr == nil {
			sk.Files = append(sk.Files, filepath.ToSlash(rel))
		}
		return nil
	})
	return sk, nil
}

// Parse splits a document into its frontmatter and its body. Only the flat
// "key: value" form is read, which is all a SKILL.md uses; a value may be
// quoted, and a folded value (key: >) continues on indented lines.
func Parse(doc string) (map[string]string, string) {
	meta := map[string]string{}
	doc = strings.TrimPrefix(doc, string(rune(0xFEFF)))
	norm := strings.ReplaceAll(doc, "\r\n", "\n")
	if !strings.HasPrefix(norm, "---\n") {
		return meta, norm
	}
	rest := norm[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return meta, norm
	}
	head, body := rest[:end], rest[end+4:]
	body = strings.TrimPrefix(strings.TrimPrefix(body, "\n"), "\n")

	var key string
	sc := bufio.NewScanner(strings.NewReader(head))
	for sc.Scan() {
		line := sc.Text()
		if key != "" && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) {
			v := strings.TrimSpace(line)
			if meta[key] != "" {
				v = meta[key] + " " + v
			}
			meta[key] = v
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if v == ">" || v == "|" || v == ">-" || v == "|-" {
			v = ""
		}
		meta[key] = unquote(v)
	}
	return meta, body
}

func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		return v[1 : len(v)-1]
	}
	return v
}

// Render writes a skill back as a SKILL.md.
func Render(sk Skill) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + sk.Name + "\n")
	b.WriteString("description: " + oneLine(sk.Description) + "\n")
	if sk.Learned {
		b.WriteString("learned: true\n")
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(sk.Body))
	b.WriteString("\n")
	return b.String()
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// GlobalDir is where global skills live: the config directory's skills
// folder, next to config.json.
func GlobalDir() string { return filepath.Join(config.Dir(), "skills") }

// ProjectDirFor is a project's skills folder; empty root, empty answer.
func ProjectDirFor(root string) string {
	if root == "" {
		return ""
	}
	return filepath.Join(config.ProjectDir(root), "skills")
}

// For is the store of a project and the global skills.
func For(root string) Store { return Store{GlobalDir: GlobalDir(), ProjectDir: ProjectDirFor(root)} }
