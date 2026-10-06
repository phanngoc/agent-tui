package engine

import (
	"strings"

	"github.com/phanngoc/agent-tui/internal/vfs"
)

// MCP servers for a CLI running in a WSL distribution.
//
// A distribution runs Windows programs through interop, so every server is
// handed to the CLI as this binary, by its /mnt path, serving it on stdio
// from the Windows side (`agent-tui mcp-proxy`, internal/mcp/proxy.go):
//
//   - A remote server (http, sse) is connected to from here, with the
//     sign-in kept here and renewed as often as the run needs. Given the URL
//     and an hour's token instead, Claude Code connected once and, when that
//     was slow, ran without the server: a scheduled Datadog log watch found
//     no Datadog.
//   - A command-line server runs its Windows command here, as it does for a
//     CLI on this machine, rather than being left out.
//   - agent-tui's own server (kit-mcp) is this binary already, and only its
//     path changes.
//
// A container has no interop, so a CLI in one gets only the remote servers,
// as they are.

// kitServer is the name agent-tui's own tools go by (kit.KitServer).
const kitServer = "agent-tui"

func serversFor(fs vfs.FS, servers map[string]any, root string) map[string]any {
	if fs == nil || fs.IsLocal() || len(servers) == 0 {
		return servers
	}
	if _, wsl := fs.(*vfs.WSL); !wsl {
		out := map[string]any{}
		for name, v := range servers {
			if def, ok := v.(map[string]any); ok && (def["type"] == "http" || def["type"] == "sse") {
				out[name] = def
			}
		}
		return out
	}
	// This binary, and the root the project's servers are read for: what
	// kit-mcp was given, which is what the turn's servers came from.
	self, _ := executable()
	if kit, ok := servers[kitServer].(map[string]any); ok {
		if cmd, _ := kit["command"].(string); cmd != "" {
			self = cmd
		}
		if args, ok := kit["args"].([]string); ok {
			for i := 0; i+1 < len(args); i++ {
				if args[i] == "-root" {
					root = args[i+1]
				}
			}
		}
	}
	exe, ok := mntPath(self)
	if !ok {
		return map[string]any{}
	}
	out := map[string]any{}
	for name, v := range servers {
		def, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if name == kitServer {
			cp := map[string]any{}
			for k, x := range def {
				cp[k] = x
			}
			cp["command"] = exe
			out[name] = cp
			continue
		}
		out[name] = map[string]any{"type": "stdio", "command": exe,
			"args": []string{"mcp-proxy", "-root", root, "-name", name}}
	}
	return out
}

// mntPath is where a distribution sees a Windows file: C:\x\y.exe is
// /mnt/c/x/y.exe under WSL's default automount. Only drive paths have one.
func mntPath(p string) (string, bool) {
	if len(p) < 3 || p[1] != ':' || (p[2] != '\\' && p[2] != '/') {
		return "", false
	}
	drive := strings.ToLower(p[:1])
	if drive < "a" || drive > "z" {
		return "", false
	}
	rest := strings.ReplaceAll(p[3:], `\`, "/")
	return "/mnt/" + drive + "/" + rest, true
}
