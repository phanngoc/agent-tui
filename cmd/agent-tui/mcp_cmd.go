package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/phanngoc/agent-tui/internal/gateway"
	"github.com/phanngoc/agent-tui/internal/kit"
	"github.com/phanngoc/agent-tui/internal/mcp"
)

// `agent-tui mcp …` signs in to remote MCP servers from a shell, for when the
// web admin is not at hand. The sign-in is the one the admin's Sign in button
// makes, kept in the same place, so the terminal, the gateway and the claude
// engine all use it.

const mcpUsage = `usage: agent-tui mcp [command] [name]

  status          the servers this folder sees, and which are signed in (default)
  login <name>    sign in to a remote server in the browser (OAuth)
  logout <name>   forget that sign-in
`

func mcpCmd(args []string) error {
	sub := "status"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	root, _ := os.Getwd()
	st := kit.MCPStore(root)
	find := func() (mcp.Server, error) {
		if len(args) == 0 {
			return mcp.Server{}, fmt.Errorf("which server? %s", "agent-tui mcp "+sub+" <name>")
		}
		for _, s := range st.List() {
			if s.Name == args[0] && !s.Shadowed {
				return s, nil
			}
		}
		return mcp.Server{}, fmt.Errorf("no MCP server named %q here (global or in %s)", args[0], root)
	}
	switch sub {
	case "status", "list":
		for _, s := range st.List() {
			if s.Shadowed {
				continue
			}
			where := s.Command
			auth := ""
			if s.Transport() != "stdio" {
				where = s.URL
				if mcp.SignedIn(s.URL) {
					auth = "  signed in"
				}
			}
			fmt.Printf("%-20s %-7s %-6s %s%s\n", s.Name, s.Scope, s.Transport(), where, auth)
		}
		return nil
	case "login":
		s, err := find()
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		l, err := mcp.BeginLogin(ctx, s)
		cancel()
		if err != nil {
			return err
		}
		fmt.Println("opening the sign-in page; if no browser opens, visit:")
		fmt.Println("  " + l.URL)
		_ = gateway.OpenBrowser(l.URL)
		if err := l.Wait(context.Background()); err != nil {
			return err
		}
		fmt.Printf("signed in to %s\n", s.Name)
		return nil
	case "logout":
		s, err := find()
		if err != nil {
			return err
		}
		if err := mcp.SignOut(s.URL); err != nil {
			return err
		}
		fmt.Printf("signed out of %s\n", s.Name)
		return nil
	}
	fmt.Fprint(os.Stderr, mcpUsage)
	return fmt.Errorf("unknown mcp command %q", sub)
}

// mcpProxy serves the server named name, as the project at root sees it.
func mcpProxy(root, name string) error {
	for _, s := range kit.MCPStore(root).List() {
		if s.Name == name && !s.Shadowed {
			return mcp.Proxy(context.Background(), os.Stdin, os.Stdout, s)
		}
	}
	return fmt.Errorf("no MCP server named %q for %s", name, root)
}
