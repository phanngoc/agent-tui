//go:build !windows

package engine

import (
	"context"

	"github.com/phanngoc/agent-tui/internal/session"
)

// Export-Clixml credentials use Windows DPAPI and cannot be decrypted here.
func conversationToken(context.Context, string) (string, session.Credential, error) {
	return "", session.Credential{}, nil
}
