package kit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/phanngoc/agent-tui/internal/agent"
)

// The browser tools drive the user's own Chrome through the agent-tui
// extension (package browser). They go through the gateway, which holds the
// extension's connection, so they work from the built-in engine and, over
// kit-mcp, from Claude Code alike. They are offered only while the extension
// is connected.

// browserConnected asks the gateway, briefly, whether Chrome is there.
func browserConnected(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var st struct {
		Connected bool `json:"connected"`
	}
	return gatewayCall(ctx, "GET", "/api/browser/status", nil, &st) == nil && st.Connected
}

// browserGuide is what the prompt says while Chrome is connected.
func browserGuide(native bool) string {
	name := func(t string) string {
		if native {
			return t
		}
		return "mcp__" + KitServer + "__" + t
	}
	return "<browser>\nThe user's own Chrome is connected. Use it when a task needs a real browser — a page behind their login, a web app to operate, something to look at. " +
		"Open pages with " + name("browser_open") + " (they go in an \"agent-tui\" tab group), read one with " + name("browser_snapshot") + " — its page text and its elements, numbered — " +
		"then act with " + name("browser_click") + " and " + name("browser_type") + " by those numbers. The numbers change after a page changes: snapshot again before the next action. " +
		"You can only touch tabs you opened and tabs the user shared. Never type passwords or payment details, and ask the user before anything that sends, buys, deletes or posts on their behalf.\n</browser>\n\n"
}

// shotPath is a screenshot's path as the agent can open it: a WSL project's
// agent reaches the host's C: drive at /mnt/c.
func (k *Kit) shotPath(p string) string {
	root := strings.ToLower(strings.ReplaceAll(k.Root, "/", `\`))
	wsl := strings.HasPrefix(root, `\\wsl.localhost\`) || strings.HasPrefix(root, `\\wsl$\`)
	if wsl && len(p) > 2 && p[1] == ':' {
		return "/mnt/" + strings.ToLower(p[:1]) + strings.ReplaceAll(p[2:], `\`, "/")
	}
	return p
}

func (k *Kit) browserTools() []agent.Extension {
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	num := func(d string) map[string]any { return map[string]any{"type": "integer", "description": d} }
	tab := num("The tab, from browser_tabs; the one last used when left out.")
	do := func(ctx context.Context, action string, in json.RawMessage) (map[string]any, error) {
		var args map[string]any
		_ = json.Unmarshal(in, &args)
		var out map[string]any
		err := gatewayCall(ctx, "POST", "/api/browser/do", map[string]any{"action": action, "args": args}, &out)
		return out, err
	}
	where := func(o map[string]any) string {
		return fmt.Sprintf("tab %v · %v\n%v", o["tab"], o["title"], o["url"])
	}
	tool := func(name, desc string, mutating bool, props map[string]any, required []string, show func(map[string]any) string) agent.Extension {
		return agent.Extension{
			Name:     name,
			Mutating: mutating,
			Def:      toolDef(name, desc, props, required...),
			Run: func(ctx context.Context, in json.RawMessage) (string, bool) {
				o, err := do(ctx, strings.TrimPrefix(name, "browser_"), in)
				if err != nil {
					return err.Error(), true
				}
				return show(o), false
			},
		}
	}
	return []agent.Extension{
		tool("browser_tabs", "List the Chrome tabs you may use: the ones you opened and the ones the user shared.", false,
			map[string]any{}, nil, func(o map[string]any) string {
				b, _ := json.MarshalIndent(o["tabs"], "", "  ")
				if string(b) == "[]" || string(b) == "null" {
					return "no tabs yet: open one with browser_open"
				}
				return string(b)
			}),
		tool("browser_open", "Open a page in the user's Chrome, in a new tab of the agent-tui group (or the current tab with new_tab false). Waits for it to load.", true,
			map[string]any{"url": str("The http(s) address."), "new_tab": map[string]any{"type": "boolean", "description": "Default true."}}, []string{"url"},
			func(o map[string]any) string { return "opened " + where(o) + "\nNext: browser_snapshot to read it." }),
		tool("browser_navigate", "Go to an address in a tab, or back, forward, or reload.", true,
			map[string]any{"tab": tab, "url": str("An http(s) address to go to."), "to": map[string]any{"type": "string", "enum": []string{"back", "forward", "reload"}}}, nil,
			func(o map[string]any) string { return where(o) }),
		tool("browser_snapshot", "Read a tab: its address, title, page text, and its interactive elements numbered [n] for browser_click and browser_type.", false,
			map[string]any{"tab": tab, "max": num("Most characters of page text; default 15000.")}, nil,
			func(o map[string]any) string {
				var b strings.Builder
				b.WriteString(where(o) + "\n")
				if sc, ok := o["scroll"].(map[string]any); ok {
					fmt.Fprintf(&b, "scrolled to %v of %v (viewport %v)\n", sc["y"], sc["height"], sc["viewport"])
				}
				b.WriteString("\n## Elements\n")
				b.WriteString(fmt.Sprint(o["elements"]))
				b.WriteString("\n\n## Page text\n")
				b.WriteString(fmt.Sprint(o["text"]))
				if t, _ := o["truncated"].(bool); t {
					b.WriteString("\n…(cut: scroll, or ask for more with max)")
				}
				return b.String()
			}),
		tool("browser_click", "Click element [ref] from the last browser_snapshot.", true,
			map[string]any{"tab": tab, "ref": num("The element's number.")}, []string{"ref"},
			func(o map[string]any) string {
				return fmt.Sprintf("clicked [%v]; now %s\nSnapshot again before the next action.", o["clicked"], where(o))
			}),
		tool("browser_type", "Type into element [ref] from the last snapshot — a text box, a text area, an editable area, or pick a select's option by its text.", true,
			map[string]any{"tab": tab, "ref": num("The element's number."), "text": str("What to type."),
				"clear":  map[string]any{"type": "boolean", "description": "Replace what is there."},
				"submit": map[string]any{"type": "boolean", "description": "Press Enter after."}}, []string{"ref", "text"},
			func(o map[string]any) string { return "typed; now " + where(o) }),
		tool("browser_key", "Press a key in a tab, on whatever has focus: Enter, Tab, Escape, ArrowDown, ArrowUp, PageDown, PageUp, Backspace, Space.", true,
			map[string]any{"tab": tab, "key": str("The key.")}, []string{"key"},
			func(o map[string]any) string { return fmt.Sprintf("pressed %v; now %s", o["pressed"], where(o)) }),
		tool("browser_scroll", "Scroll a tab: up, down (a screen, or amount pixels), top or bottom.", false,
			map[string]any{"tab": tab, "direction": map[string]any{"type": "string", "enum": []string{"down", "up", "top", "bottom"}}, "amount": num("Pixels.")}, nil,
			func(o map[string]any) string {
				return fmt.Sprintf("scrolled to %v of %v; %s", o["y"], o["height"], where(o))
			}),
		tool("browser_screenshot", "Take a screenshot of a tab, saved as a PNG file you can open to look at.", false,
			map[string]any{"tab": tab}, nil,
			func(o map[string]any) string {
				return fmt.Sprintf("saved %s\n(%s) — open the file to see it.", k.shotPath(fmt.Sprint(o["path"])), where(o))
			}),
		tool("browser_close", "Close a tab you opened.", true,
			map[string]any{"tab": tab}, nil,
			func(o map[string]any) string { return fmt.Sprintf("closed tab %v", o["closed"]) }),
	}
}
