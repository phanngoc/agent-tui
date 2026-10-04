// Package claudecode finds Claude Code installations on this machine — the
// host's, and on Windows each WSL distribution's — and reads what they keep:
// skills, MCP servers and per-project memory.
//
// agent-tui keeps its own skills, servers and memory and never writes here.
// This is the other direction: a way to bring what has already been built for
// Claude Code across, instead of writing it again.
package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/phanngoc/agent-tui/internal/skill"
)

// Install is one home directory with a .claude folder in it.
type Install struct {
	// ID is "host" or "wsl:<distro>".
	ID    string `json:"id"`
	Label string `json:"label"`
	// Home is the home directory as this process reaches it: a UNC path
	// (\\wsl.localhost\Ubuntu\home\me) for a WSL one.
	Home string `json:"home"`
	// LinuxHome is the home as the distribution itself spells it, which is
	// what its project paths are written in.
	LinuxHome string `json:"linux_home,omitempty"`
}

// Installs lists the Claude Code homes found. A home without a .claude folder
// is left out.
func Installs(ctx context.Context) []Install {
	var out []Install
	if home, err := os.UserHomeDir(); err == nil && isDir(filepath.Join(home, ".claude")) {
		out = append(out, Install{ID: "host", Label: "this machine", Home: home})
	}
	if runtime.GOOS == "windows" {
		out = append(out, wslInstalls(ctx)...)
	}
	return out
}

// Find returns the install with this id.
func Find(ctx context.Context, id string) (Install, bool) {
	for _, in := range Installs(ctx) {
		if in.ID == id {
			return in, true
		}
	}
	return Install{}, false
}

func wslInstalls(ctx context.Context) []Install {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "wsl.exe", "-l", "-q")
	cmd.Env = append(os.Environ(), "WSL_UTF8=1")
	raw, err := cmd.Output()
	if err != nil {
		return nil
	}
	var out []Install
	for _, d := range strings.Split(decodeWSL(raw), "\n") {
		d = strings.TrimSpace(d)
		if d == "" || strings.HasPrefix(strings.ToLower(d), "docker-desktop") {
			continue
		}
		c := exec.CommandContext(ctx, "wsl.exe", "-d", d, "--", "sh", "-c", `printf %s "$HOME"`)
		c.Env = append(os.Environ(), "WSL_UTF8=1")
		h, err := c.Output()
		if err != nil {
			continue
		}
		lh := strings.TrimSpace(string(h))
		if lh == "" {
			continue
		}
		unc := `\\wsl.localhost\` + d + strings.ReplaceAll(lh, "/", `\`)
		if !isDir(filepath.Join(unc, ".claude")) {
			continue
		}
		out = append(out, Install{ID: "wsl:" + d, Label: "WSL " + d, Home: unc, LinuxHome: lh})
	}
	return out
}

// decodeWSL reads wsl.exe's list output, which is UTF-16 unless WSL_UTF8 is
// honoured — older builds ignore it.
func decodeWSL(b []byte) string {
	if len(b) >= 2 && (b[1] == 0 || b[0] == 0xff) {
		var r []rune
		for i := 0; i+1 < len(b); i += 2 {
			c := rune(b[i]) | rune(b[i+1])<<8
			if c == 0xfeff {
				continue
			}
			r = append(r, c)
		}
		return strings.ReplaceAll(string(r), "\r", "")
	}
	return strings.ReplaceAll(string(b), "\r", "")
}

// SkillRef is a skill found in an install.
type SkillRef struct {
	Install     string `json:"install"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Dir         string `json:"dir"`
}

// Skills lists the user-level skills of an install.
func (in Install) Skills() []SkillRef {
	dir := filepath.Join(in.Home, ".claude", "skills")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []SkillRef
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if !isDir(p) {
			continue
		}
		sk, err := skill.Read(filepath.Join(p, "SKILL.md"), skill.Global)
		if err != nil {
			continue
		}
		out = append(out, SkillRef{Install: in.ID, Name: sk.Name, Description: sk.Description, Dir: p})
	}
	return out
}

// Server is an MCP server definition as Claude Code writes it.
type Server struct {
	Install string            `json:"install"`
	Name    string            `json:"name"`
	Project string            `json:"project,omitempty"` // empty for a user-level one
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// Servers lists the MCP servers in an install's ~/.claude.json: the user-level
// ones and every project's.
func (in Install) Servers() []Server {
	b, err := os.ReadFile(filepath.Join(in.Home, ".claude.json"))
	if err != nil {
		return nil
	}
	var doc struct {
		MCPServers map[string]Server `json:"mcpServers"`
		Projects   map[string]struct {
			MCPServers map[string]Server `json:"mcpServers"`
		} `json:"projects"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return nil
	}
	var out []Server
	add := func(project string, m map[string]Server) {
		for name, s := range m {
			s.Install, s.Name, s.Project = in.ID, name, project
			out = append(out, s)
		}
	}
	add("", doc.MCPServers)
	for p, v := range doc.Projects {
		add(p, v.MCPServers)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// MemoryFile is one of Claude Code's per-project memory notes.
type MemoryFile struct {
	Install     string `json:"install"`
	Project     string `json:"project"` // the folder name under projects/
	File        string `json:"file"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Body        string `json:"body"`
}

// Memory reads every memory note of an install, MEMORY.md indexes excepted.
func (in Install) Memory() []MemoryFile {
	base := filepath.Join(in.Home, ".claude", "projects")
	projects, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var out []MemoryFile
	for _, p := range projects {
		dir := filepath.Join(base, p.Name(), "memory")
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".md") || f.Name() == "MEMORY.md" {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, f.Name()))
			if err != nil {
				continue
			}
			meta, body := skill.Parse(string(b))
			typ := meta["type"]
			if typ == "" {
				typ = meta["metadata.type"]
			}
			if typ == "" {
				// Newer notes nest it: "metadata:\n  type: feedback" folds
				// into metadata = "type: feedback".
				if _, v, ok := strings.Cut(meta["metadata"], "type:"); ok {
					typ = strings.Fields(v + " ")[0]
				}
			}
			out = append(out, MemoryFile{
				Install: in.ID, Project: p.Name(), File: f.Name(),
				Name: meta["name"], Description: meta["description"], Type: typ,
				Body: strings.TrimSpace(body),
			})
		}
	}
	return out
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
