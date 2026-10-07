package engine

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
)

//go:embed tokenpool.ps1
var tokenPoolScript string

func conversationToken(ctx context.Context, conversation string) (string, error) {
	if conversation == "" {
		return "", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("Claude token pool: cannot locate user home")
	}
	return readConversationToken(ctx, filepath.Join(home, ".claude", "token-rotation"), conversation)
}

func readConversationToken(ctx context.Context, root, conversation string) (string, error) {
	if _, err := os.Stat(filepath.Join(root, "pool.xml")); errors.Is(err, os.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", errors.New("Claude token pool: cannot read pool.xml")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// PowerShell does not follow CommandLineToArgvW's quoting rules for
	// -Command. Encoding the fixed script preserves its quotes and newlines.
	units := utf16.Encode([]rune(tokenPoolScript))
	encoded := make([]byte, 2*len(units))
	for i, unit := range units {
		binary.LittleEndian.PutUint16(encoded[2*i:], unit)
	}
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(encoded))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Env = append(os.Environ(), "AGENT_TUI_TOKEN_POOL="+root, "AGENT_TUI_CONVERSATION="+conversation)
	// Never surface PowerShell diagnostics: a malformed credential can be
	// quoted in an exception. Stdout is a private pipe, not a log.
	out, err := cmd.Output()
	if err != nil {
		return "", errors.New("Claude token pool: could not decrypt or assign a token; check pool.xml and agent-tui-state.json")
	}
	token := strings.TrimSpace(string(out))
	if !strings.HasPrefix(token, "sk-ant-oat") || strings.ContainsAny(token, "\r\n\x00") {
		return "", errors.New("Claude token pool: invalid OAuth credential")
	}
	return token, nil
}
