package engine

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"

	"github.com/phanngoc/agent-tui/internal/session"
)

//go:embed tokenpool.ps1
var tokenPoolScript string

func conversationToken(ctx context.Context, conversation string) (string, session.Credential, error) {
	if conversation == "" {
		return "", session.Credential{}, nil
	}
	root := poolDir()
	if root == "" {
		return "", session.Credential{}, errors.New("Claude token pool: cannot locate user home")
	}
	return readConversationToken(ctx, root, conversation)
}

func readConversationToken(ctx context.Context, root, conversation string) (string, session.Credential, error) {
	var none session.Credential
	if _, err := os.Stat(filepath.Join(root, "pool.xml")); errors.Is(err, os.ErrNotExist) {
		return "", none, nil
	} else if err != nil {
		return "", none, errors.New("Claude token pool: cannot read pool.xml")
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
		return "", none, errors.New("Claude token pool: could not decrypt or assign a token; check pool.xml and agent-tui-state.json")
	}
	head, token, _ := strings.Cut(string(out), "\n")
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, "sk-ant-oat") || strings.ContainsAny(token, "\r\n\x00") {
		return "", none, errors.New("Claude token pool: invalid OAuth credential")
	}
	var c session.Credential
	if n, _ := fmt.Sscanf(strings.TrimSpace(head), "%d %d %s", &c.Slot, &c.Of, &c.ID); n != 3 {
		return "", none, errors.New("Claude token pool: the pool script named no token")
	}
	return token, c, nil
}
