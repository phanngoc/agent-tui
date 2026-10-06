package lsp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/vfs"
)

// A real typescript-language-server, installed on first use, sees the whole
// project: an error that needs another file's types is reported, and
// completion offers that file's export. It needs npm and the network the
// first time, so it runs only when asked: AGENT_TUI_LSP_TEST=1.
func TestTypeScriptServerSeesTheProject(t *testing.T) {
	if os.Getenv("AGENT_TUI_LSP_TEST") == "" {
		t.Skip("set AGENT_TUI_LSP_TEST=1 to run the language server")
	}
	if dd := os.Getenv("AGENT_TUI_LSP_DATA"); dd != "" {
		t.Setenv("XDG_DATA_HOME", dd) // reuse an install between runs
	}
	root := t.TempDir()
	// A path alias, as Next and Vite projects have: only a server reading the
	// tsconfig resolves it.
	_ = os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte(`{"compilerOptions":{"strict":true,"target":"es2022","module":"esnext","moduleResolution":"bundler","baseUrl":".","paths":{"@/*":["./src/*"]}}}`), 0o644)
	_ = os.MkdirAll(filepath.Join(root, "src", "lib"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "src", "lib", "util.ts"), []byte("export function greet(name: string): string { return 'hi ' + name }\n"), 0o644)
	main := "import { greet } from '@/lib/util'\nconst n: number = greet('x')\ngre\n"
	_ = os.WriteFile(filepath.Join(root, "main.ts"), []byte(main), 0o644)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	s, err := Start(ctx, Spec{Server: "typescript", Root: root, FS: vfs.NewLocal(root), Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	send := func(v map[string]any) {
		v["jsonrpc"] = "2.0"
		b, _ := json.Marshal(v)
		if err := s.Send(b); err != nil {
			t.Fatal(err)
		}
	}
	mainURI := s.RootURI + "/main.ts"
	send(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"processId": nil, "rootUri": s.RootURI,
		"workspaceFolders":      []map[string]any{{"uri": s.RootURI, "name": "p"}},
		"initializationOptions": s.InitOptions,
		"capabilities":          map[string]any{"textDocument": map[string]any{"publishDiagnostics": map[string]any{}, "completion": map[string]any{}}}}})
	wait := func(match func(m map[string]any) bool, what string) map[string]any {
		deadline := time.After(90 * time.Second)
		for {
			select {
			case b, ok := <-s.Messages():
				if !ok {
					t.Fatalf("the server ended waiting for %s: %s", what, s.Err())
				}
				var m map[string]any
				_ = json.Unmarshal(b, &m)
				// Requests from the server get an empty answer.
				if id, ok := m["id"]; ok && m["method"] != nil {
					b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": nil})
					_ = s.Send(b)
				}
				if match(m) {
					return m
				}
			case <-deadline:
				t.Fatalf("no %s: %s", what, s.Err())
			}
		}
	}
	wait(func(m map[string]any) bool { return m["id"] == float64(1) }, "initialize result")
	send(map[string]any{"method": "initialized", "params": map[string]any{}})
	send(map[string]any{"method": "textDocument/didOpen", "params": map[string]any{"textDocument": map[string]any{"uri": mainURI, "languageId": "typescript", "version": 1, "text": main}}})

	diag := wait(func(m map[string]any) bool {
		if m["method"] != "textDocument/publishDiagnostics" {
			return false
		}
		p := m["params"].(map[string]any)
		return strings.HasSuffix(p["uri"].(string), "/main.ts") && len(p["diagnostics"].([]any)) > 0
	}, "diagnostics for main.ts")
	text, _ := json.Marshal(diag["params"])
	if !strings.Contains(string(text), "not assignable to type 'number'") {
		t.Fatalf("the cross-file type error was not seen: %s", text)
	}

	send(map[string]any{"id": 2, "method": "textDocument/completion", "params": map[string]any{"textDocument": map[string]any{"uri": mainURI}, "position": map[string]any{"line": 2, "character": 3}}})
	comp := wait(func(m map[string]any) bool { return m["id"] == float64(2) }, "completion")
	b, _ := json.Marshal(comp["result"])
	if !strings.Contains(string(b), `"label":"greet"`) {
		t.Fatalf("completion does not offer the imported greet: %.400s", b)
	}

	// Go to definition from a use goes through the import to util.ts.
	send(map[string]any{"id": 3, "method": "textDocument/definition", "params": map[string]any{"textDocument": map[string]any{"uri": mainURI}, "position": map[string]any{"line": 1, "character": 20}}})
	def := wait(func(m map[string]any) bool { return m["id"] == float64(3) }, "definition")
	b, _ = json.Marshal(def["result"])
	if !strings.Contains(string(b), "src/lib/util.ts") {
		t.Fatalf("definition did not reach util.ts: %s", b)
	}
}

func TestFileURI(t *testing.T) {
	if got := fileURI(`C:\Users\me\my app`, false); got != "file:///C:/Users/me/my%20app" {
		t.Errorf("windows: %s", got)
	}
	if got := fileURI("/home/me/app/", true); got != "file:///home/me/app" {
		t.Errorf("linux: %s", got)
	}
}

func TestLongPathSpellsShortNamesOut(t *testing.T) {
	dir := t.TempDir()
	if got := longPath(dir); got == "" || !strings.EqualFold(filepath.Clean(got), filepath.Clean(got)) {
		t.Fatalf("longPath(%q) = %q", dir, got)
	}
	if strings.Contains(dir, "~") && strings.Contains(longPath(dir), "~") {
		t.Fatalf("a short name survived: %q", longPath(dir))
	}
}
