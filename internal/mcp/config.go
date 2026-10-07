// Package mcp keeps the MCP servers the agent may use and talks to them.
//
// Servers are configured in the shape Claude Code uses — {"mcpServers": {name:
// {...}}} — in two files: a global one in the config directory and a project
// one in the project's .agent-tui folder. A project server with the same name
// as a global one replaces it in that project.
//
// The claude engine is handed the merged list as an --mcp-config and runs the
// servers itself; the built-in engine connects to them through Client and
// offers their tools to the model as mcp__<server>__<tool>.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Scope says which file a server is defined in.
type Scope string

const (
	Global  Scope = "global"
	Project Scope = "project"
)

// Server is one MCP server definition.
type Server struct {
	Name string `json:"name,omitempty"`
	// Type is stdio, http or sse. Empty means stdio when a command is given
	// and http when a URL is.
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// Disabled keeps a definition without starting it.
	Disabled bool `json:"disabled,omitempty"`

	Scope    Scope `json:"scope,omitempty"`
	Shadowed bool  `json:"shadowed,omitempty"`
}

// WithoutAuthorization is s without an Authorization header of its own, so a
// sign-in supplies it; ok says there was one to drop.
func (s Server) WithoutAuthorization() (Server, bool) {
	h := map[string]string{}
	ok := false
	for k, v := range s.Headers {
		if strings.EqualFold(k, "Authorization") {
			ok = true
			continue
		}
		h[k] = v
	}
	if !ok {
		return s, false
	}
	if len(h) == 0 {
		h = nil
	}
	s.Headers = h
	return s, true
}

// Transport resolves Type.
func (s Server) Transport() string {
	switch s.Type {
	case "stdio", "http", "sse":
		return s.Type
	case "streamable-http", "streamableHttp":
		return "http"
	}
	if s.Command != "" {
		return "stdio"
	}
	return "http"
}

// Validate reports what is missing from a definition.
func (s Server) Validate() error {
	if !nameRE.MatchString(s.Name) {
		return fmt.Errorf("%q is not a valid server name: use letters, digits, '_' and '-'", s.Name)
	}
	switch s.Transport() {
	case "stdio":
		if s.Command == "" {
			return errors.New("a stdio server needs a command")
		}
	default:
		if s.URL == "" {
			return errors.New("an http server needs a url")
		}
	}
	return nil
}

var nameRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type file struct {
	MCPServers map[string]Server `json:"mcpServers"`
}

// Store reads and writes both files.
type Store struct {
	GlobalPath  string
	ProjectPath string // empty when there is no project
}

func (st Store) path(scope Scope) (string, error) {
	switch scope {
	case Global:
		return st.GlobalPath, nil
	case Project:
		if st.ProjectPath == "" {
			return "", errors.New("no project is open")
		}
		return st.ProjectPath, nil
	}
	return "", fmt.Errorf("unknown scope %q", scope)
}

func read(path string, scope Scope) []Server {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var f file
	if json.Unmarshal(b, &f) != nil {
		return nil
	}
	out := make([]Server, 0, len(f.MCPServers))
	for name, s := range f.MCPServers {
		s.Name, s.Scope = name, scope
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func write(path string, servers []Server) error {
	f := file{MCPServers: map[string]Server{}}
	for _, s := range servers {
		name := s.Name
		s.Name, s.Scope, s.Shadowed = "", "", false
		f.MCPServers[name] = s
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// List returns both scopes, project servers first; a global server a project
// one replaces is marked Shadowed.
func (st Store) List() []Server {
	proj := read(st.ProjectPath, Project)
	seen := map[string]bool{}
	for _, s := range proj {
		seen[s.Name] = true
	}
	out := proj
	for _, s := range read(st.GlobalPath, Global) {
		s.Shadowed = seen[s.Name]
		out = append(out, s)
	}
	return out
}

// Active is what a session gets: neither shadowed, nor disabled in the file,
// nor switched off for this project.
func (st Store) Active(off []string) []Server {
	var out []Server
	for _, s := range st.List() {
		if s.Shadowed || s.Disabled || contains(off, s.Name) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// Save creates or replaces one server in its scope.
func (st Store) Save(s Server) error {
	if err := s.Validate(); err != nil {
		return err
	}
	path, err := st.path(s.Scope)
	if err != nil {
		return err
	}
	list := read(path, s.Scope)
	replaced := false
	for i := range list {
		if list[i].Name == s.Name {
			list[i], replaced = s, true
		}
	}
	if !replaced {
		list = append(list, s)
	}
	return write(path, list)
}

// DropAuthorization removes the Authorization header of the saved server
// named name in scope, once a sign-in has taken its place; ok says there was
// one to drop.
func (st Store) DropAuthorization(scope Scope, name string) (bool, error) {
	for _, s := range st.List() {
		if s.Name != name || s.Scope != scope {
			continue
		}
		s, ok := s.WithoutAuthorization()
		if !ok {
			return false, nil
		}
		return true, st.Save(s)
	}
	return false, nil
}

// Delete removes one server from its scope.
func (st Store) Delete(scope Scope, name string) error {
	path, err := st.path(scope)
	if err != nil {
		return err
	}
	list := read(path, scope)
	out := list[:0]
	found := false
	for _, s := range list {
		if s.Name == name {
			found = true
			continue
		}
		out = append(out, s)
	}
	if !found {
		return fmt.Errorf("no %s server named %q", scope, name)
	}
	return write(path, out)
}

// WithSignIn hands each remote server agent-tui is signed in to its token as
// an Authorization header, for the claude engine, which runs the servers
// itself and has no access to agent-tui's sign-ins. A definition with its own
// Authorization header keeps it.
func WithSignIn(ctx context.Context, servers []Server) []Server {
	out := make([]Server, len(servers))
	for i, s := range servers {
		out[i] = s
		if s.Transport() == "stdio" {
			continue
		}
		t := &httpTransport{url: s.URL, headers: s.Headers}
		if t.ownAuth() {
			continue
		}
		if tok := accessToken(ctx, s.URL, false); tok != "" {
			h := make(map[string]string, len(s.Headers)+1)
			for k, v := range s.Headers {
				h[k] = v
			}
			h["Authorization"] = "Bearer " + tok
			out[i].Headers = h
		}
	}
	return out
}

// ClaudeConfig renders servers as an --mcp-config payload for Claude Code.
func ClaudeConfig(servers []Server) map[string]any {
	m := map[string]any{}
	for _, s := range servers {
		d := map[string]any{"type": s.Transport()}
		if s.Transport() == "stdio" {
			d["command"] = s.Command
			if len(s.Args) > 0 {
				d["args"] = s.Args
			}
			if len(s.Env) > 0 {
				d["env"] = s.Env
			}
		} else {
			d["url"] = s.URL
			if len(s.Headers) > 0 {
				d["headers"] = s.Headers
			}
		}
		m[s.Name] = d
	}
	return m
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
