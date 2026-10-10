package browser

import (
	"embed"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/phanngoc/agent-tui/internal/config"
)

//go:embed extension
var extension embed.FS

// ExtensionDir is where the extension's folder is written, for Chrome's
// "Load unpacked".
func ExtensionDir() string { return filepath.Join(config.DataDir(), "browser-extension") }

// WriteExtension writes the extension into dir, its config.js pointing at
// gateway — so the folder works for the gateway that made it, whatever port
// that one runs on. Written again, it is brought up to date in place, and
// Chrome picks it up on the extension's reload.
func WriteExtension(dir, gateway string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	err := fs.WalkDir(extension, "extension", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := extension.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("extension", filepath.FromSlash(p))
		return os.WriteFile(filepath.Join(dir, rel), b, 0o644)
	})
	if err != nil {
		return err
	}
	cfg, _ := json.Marshal(map[string]string{"gateway": gateway})
	return os.WriteFile(filepath.Join(dir, "config.js"), []byte("self.AGENT_TUI = "+string(cfg)+";\n"), 0o644)
}
