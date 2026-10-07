package engine

import (
	"context"
	"strings"
)

// Only a child gets the selected credential. Neither the process environment
// nor a session/transcript ever contains it.
func claudeTokenEnv(ctx context.Context, env []string, conversation string) ([]string, error) {
	token, err := conversationToken(ctx, conversation)
	if err != nil || token == "" {
		return env, err
	}
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(key) {
		case "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN":
			continue
		}
		out = append(out, entry)
	}
	return append(out, "CLAUDE_CODE_OAUTH_TOKEN="+token), nil
}
