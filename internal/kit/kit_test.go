package kit

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phanngoc/agent-tui/internal/config"
	"github.com/phanngoc/agent-tui/internal/memory"
	"github.com/phanngoc/agent-tui/internal/skill"
)

func TestExtrasAssembleAndTrace(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Run make test."), 0o644)
	_ = config.SaveProjectSettings(root, config.ProjectSettings{Instructions: "Answer briefly.", DisabledSkills: []string{"off"}})
	st := skill.For(root)
	_, _ = st.Save(skill.Skill{Name: "deploy", Description: "install globally", Body: "steps", Scope: skill.Project})
	_, _ = st.Save(skill.Skill{Name: "off", Description: "switched off", Body: "x", Scope: skill.Global})
	bank := memory.For(root)
	_, _ = bank.Project.Put(memory.Record{Content: "Deploys go through install.ps1", Type: memory.TypeWorkMethod, Priority: 80})

	k := For(root)
	k.DryRun = true
	x, tr := k.Extras(context.Background(), "api", "how do I deploy this?")
	for _, want := range []string{"Answer briefly.", "Run make test.", "deploy (project)", "install.ps1", "<relevant-memories>"} {
		if !strings.Contains(x.System, want) {
			t.Errorf("system lacks %q:\n%s", want, x.System)
		}
	}
	if strings.Contains(x.System, "switched off") {
		t.Error("a disabled skill was listed")
	}
	if len(tr.Recalled) != 1 || tr.Skills[0] != "deploy" || tr.Chars != len(x.System) {
		t.Fatalf("trace: %+v", tr)
	}
	if bank.Project.All()[0].Hits != 0 {
		t.Fatal("a dry run counted a hit")
	}

	tools := map[string]func(context.Context, json.RawMessage) (string, bool){}
	for _, e := range x.Tools {
		tools[e.Name] = e.Run
	}
	if out, isErr := tools["skill"](context.Background(), json.RawMessage(`{"name":"deploy"}`)); isErr || !strings.Contains(out, "steps") {
		t.Fatalf("skill tool: %q", out)
	}
	if out, isErr := tools["memory_save"](context.Background(), json.RawMessage(`{"content":"Always use tabs","type":"instruction"}`)); isErr || !strings.Contains(out, "global") {
		t.Fatalf("memory_save: %q", out)
	}
	if out, _ := tools["memory_search"](context.Background(), json.RawMessage(`{"query":"tabs"}`)); !strings.Contains(out, "Always use tabs") {
		t.Fatalf("memory_search: %q", out)
	}

	SaveTrace(Trace{Session: "s1", Prompt: "p"})
	if got := Traces("s1"); len(got) != 1 || got[0].Prompt != "p" {
		t.Fatalf("traces: %+v", got)
	}
	if got := Traces("../etc"); len(got) != 0 {
		t.Fatal("a path escaped the trace folder")
	}
}

func TestServeMCPOffersTheKitTools(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"memory_save","arguments":{"content":"Prefers tabs","type":"persona"}}}`,
	}, "\n") + "\n"
	var out strings.Builder
	if err := ServeMCP(context.Background(), root, "", strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("answers: %q", out.String())
	}
	for _, want := range []string{`"skill"`, `"memory_search"`, `"inputSchema"`} {
		if !strings.Contains(lines[1], want) {
			t.Errorf("tools/list lacks %s: %s", want, lines[1])
		}
	}
	if !strings.Contains(lines[2], "saved mem_") || len(memory.For(root).Global.All()) != 1 {
		t.Fatalf("call: %s", lines[2])
	}

	_, tr := For(root).Extras(context.Background(), "claude", "x")
	if len(tr.Tools) == 0 || !strings.HasPrefix(tr.Tools[0], "mcp__agent-tui__") {
		t.Fatalf("claude trace tools: %v", tr.Tools)
	}
}
