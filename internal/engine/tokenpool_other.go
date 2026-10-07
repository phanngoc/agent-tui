//go:build !windows

package engine

import "context"

// Export-Clixml credentials use Windows DPAPI and cannot be decrypted here.
func conversationToken(context.Context, string) (string, error) { return "", nil }
