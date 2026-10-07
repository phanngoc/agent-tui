package engine

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fakeTokenPool(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `
$ErrorActionPreference='Stop'
@('sk-ant-oat-fake-a','sk-ant-oat-fake-b','sk-ant-oat-fake-c') | ForEach-Object {
 New-Object System.Management.Automation.PSCredential('test', (ConvertTo-SecureString $_ -AsPlainText -Force))
} | Export-Clixml -LiteralPath (Join-Path $env:TEST_POOL 'pool.xml')`)
	cmd.Env = append(os.Environ(), "TEST_POOL="+root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	return root
}

func TestTokenPoolRoundRobinResumeAndConcurrentProcesses(t *testing.T) {
	root := fakeTokenPool(t)
	pick := func(id string) string {
		t.Helper()
		token, _, err := readConversationToken(context.Background(), root, id)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	for i, id := range []string{"a", "b", "a", "c", "d"} {
		want := []string{"a", "b", "a", "c", "a"}[i]
		if got := pick(id); got != "sk-ant-oat-fake-"+want {
			t.Fatalf("assignment %s did not follow round robin", id)
		}
	}
	// The token is named by its place and fingerprint, never by itself.
	_, cred, err := readConversationToken(context.Background(), root, "b")
	if err != nil {
		t.Fatal(err)
	}
	if cred.Slot != 2 || cred.Of != 3 || len(cred.ID) != 12 || strings.Contains(cred.ID, "sk-ant") {
		t.Fatalf("credential name: %+v", cred)
	}
	// Each call launches a separate PowerShell process; the named mutex must
	// prevent two terminals/scheduler runs from claiming the same cursor slot.
	var wg sync.WaitGroup
	results := make(chan string, 6)
	for _, id := range []string{"e", "f", "g", "h", "i", "j"} {
		wg.Go(func() {
			token, _, err := readConversationToken(context.Background(), root, id)
			if err != nil {
				t.Error(err)
				return
			}
			results <- token
		})
	}
	wg.Wait()
	close(results)
	counts := map[string]int{}
	for token := range results {
		counts[token]++
	}
	for _, suffix := range []string{"a", "b", "c"} {
		if counts["sk-ant-oat-fake-"+suffix] != 2 {
			t.Fatal("concurrent assignments are not balanced")
		}
	}
	if pick("a") != "sk-ant-oat-fake-a" {
		t.Fatal("resume changed token")
	}
	data, err := os.ReadFile(filepath.Join(root, "agent-tui-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "sk-ant") {
		t.Fatal("plaintext credential persisted")
	}
	var state struct{ Assignments map[string]string }
	if err := json.Unmarshal([]byte(strings.TrimPrefix(string(data), "\ufeff")), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Assignments) != 10 {
		t.Fatalf("lost assignments: %d", len(state.Assignments))
	}
	// The pool is sized from the file Export-Clixml wrote, without opening
	// a credential.
	if r := poolUsage(root, time.Now()); !r.Enabled || r.Size != 3 || len(r.Tokens) != 3 {
		t.Fatalf("pool report: %+v", r)
	}
}

func TestTokenPoolMissingCorruptAndEnvironment(t *testing.T) {
	if token, _, err := readConversationToken(context.Background(), t.TempDir(), "a"); err != nil || token != "" {
		t.Fatal("missing pool must retain existing authentication")
	}
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(home, ".claude", "token-rotation")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	fixture := fakeTokenPool(t)
	data, _ := os.ReadFile(filepath.Join(fixture, "pool.xml"))
	if err := os.WriteFile(filepath.Join(root, "pool.xml"), data, 0600); err != nil {
		t.Fatal(err)
	}
	before := []string{"PATH=keep", "ANTHROPIC_API_KEY=old", "anthropic_auth_token=old", "CLAUDE_CODE_OAUTH_TOKEN=old"}
	got, cred, err := claudeTokenEnv(context.Background(), before, "conversation")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "PATH=keep" || got[1] != "CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat-fake-a" {
		t.Fatal("credential precedence incorrect")
	}
	if cred.Slot != 1 || cred.Of != 3 {
		t.Fatalf("credential name: %+v", cred)
	}
	if before[1] != "ANTHROPIC_API_KEY=old" {
		t.Fatal("input environment mutated")
	}
	if err := os.WriteFile(filepath.Join(root, "agent-tui-state.json"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readConversationToken(context.Background(), root, "b"); err == nil || strings.Contains(err.Error(), "sk-ant") {
		t.Fatal("corrupt state must fail without exposing secrets")
	}
}
