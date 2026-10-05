package engine

import (
	"strings"

	"github.com/phanngoc/agent-tui/internal/vfs"
)

// MCP servers for a CLI running in a WSL distribution.
//
// They used to be dropped for any CLI not running on this machine, on the
// reasoning that their commands exist here and not there. That threw out far
// more than it had to, and it showed: Claude Code in a distribution had no
// skill, memory or schedule tools, so asked to "run this every hour" it wrote
// itself a crontab entry nobody could see; and it had none of the remote
// servers either, though a URL is the same from anywhere.
//
//   - A remote server (http, sse) is kept as it is, signed-in token and all.
//   - agent-tui's own server (kit-mcp) is this Windows binary, which a
//     distribution runs through interop by its /mnt path; it serves the
//     project's tools from the Windows side, where the project's memory,
//     skills and the gateway are.
//   - Any other command-line server is left out: its command is a Windows
//     one, and starting it from Linux is a guess.
//
// A container has no interop, so a CLI in one still gets only remote servers.

// kitServer is the name agent-tui's own tools go by (kit.KitServer).
const kitServer = "agent-tui"

func serversFor(fs vfs.FS, servers map[string]any) map[string]any {
	if fs == nil || fs.IsLocal() || len(servers) == 0 {
		return servers
	}
	_, wsl := fs.(*vfs.WSL)
	out := map[string]any{}
	for name, v := range servers {
		def, ok := v.(map[string]any)
		if !ok {
			continue
		}
		switch def["type"] {
		case "http", "sse":
			out[name] = def
		case "stdio", nil:
			if name != kitServer || !wsl {
				continue
			}
			cmd, _ := def["command"].(string)
			p, ok := mntPath(cmd)
			if !ok {
				continue
			}
			cp := map[string]any{}
			for k, x := range def {
				cp[k] = x
			}
			cp["command"] = p
			out[name] = cp
		}
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
