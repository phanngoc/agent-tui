package learn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/phanngoc/agent-tui/internal/engine"
)

// LLM is the one thing learning needs from a model: a system prompt and a
// user message in, text out.
type LLM interface {
	Complete(ctx context.Context, system, user string, maxTokens int64) (string, error)
	Name() string
}

// DefaultModel does the extracting. Learning runs after turns, in the
// background, on every session: it should be fast and cheap, and judging what
// is worth remembering does not need the strongest model.
const DefaultModel = "claude-haiku-4-5"

// NewLLM picks how to reach a model: the API when there is a credential for
// it, otherwise an installed Claude Code CLI, which brings its own login.
func NewLLM(model string) (LLM, error) {
	if model == "" {
		model = DefaultModel
	}
	if engine.HasAPICredentials() {
		return &apiLLM{client: anthropic.NewClient(), model: model}, nil
	}
	if bin, err := exec.LookPath("claude"); err == nil {
		return &cliLLM{bin: bin, model: cliModel(model)}, nil
	}
	return nil, errors.New("no model to learn with: set ANTHROPIC_API_KEY, run `ant auth login`, or install Claude Code")
}

type apiLLM struct {
	client anthropic.Client
	model  string
}

func (a *apiLLM) Name() string { return "api · " + a.model }

func (a *apiLLM) Complete(ctx context.Context, system, user string, maxTokens int64) (string, error) {
	msg, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: maxTokens,
		System:    []anthropic.TextBlockParam{{Text: system}},
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(user))},
	})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, block := range msg.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String(), nil
}

// cliLLM asks Claude Code in print mode, with no tools, no MCP and no settings
// of its own: only the prompt we give it.
type cliLLM struct {
	bin   string
	model string
}

func (c *cliLLM) Name() string { return "claude CLI · " + c.model }

// cliModel turns an API model id into the alias the CLI takes.
func cliModel(m string) string {
	switch {
	case strings.Contains(m, "haiku"):
		return "haiku"
	case strings.Contains(m, "sonnet"):
		return "sonnet"
	case strings.Contains(m, "opus"):
		return "opus"
	}
	return m
}

func (c *cliLLM) Complete(ctx context.Context, system, user string, _ int64) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	// The prompt goes on stdin: a transcript is longer than a Windows command
	// line may be.
	cmd := exec.CommandContext(ctx, c.bin, "-p",
		"--output-format", "json",
		"--model", c.model,
		"--no-session-persistence",
		"--strict-mcp-config",
		"--setting-sources", "",
		"--system-prompt", system,
		"--tools", "")
	cmd.Stdin = strings.NewReader(user)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude: %v: %s", err, strings.TrimSpace(errb.String()))
	}
	var res struct {
		Result  string `json:"result"`
		IsError bool   `json:"is_error"`
	}
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		return "", fmt.Errorf("claude: unreadable answer: %.200s", out.String())
	}
	if res.IsError {
		return "", errors.New("claude: " + res.Result)
	}
	return res.Result, nil
}

var (
	thinkRE = regexp.MustCompile(`(?s)<think>.*?</think>`)
	fenceRE = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)```")
)

// decodeJSON reads a model's JSON answer, forgiving the usual wrapping: a
// reasoning block, a code fence, prose before or after.
func decodeJSON(text string, v any) error {
	text = thinkRE.ReplaceAllString(text, "")
	if m := fenceRE.FindStringSubmatch(text); m != nil {
		text = m[1]
	}
	text = strings.TrimSpace(text)
	if err := json.Unmarshal([]byte(text), v); err == nil {
		return nil
	}
	// Cut from the first bracket to its last partner.
	for _, pair := range [][2]string{{"[", "]"}, {"{", "}"}} {
		i, j := strings.Index(text, pair[0]), strings.LastIndex(text, pair[1])
		if i >= 0 && j > i {
			if err := json.Unmarshal([]byte(text[i:j+1]), v); err == nil {
				return nil
			}
		}
	}
	return fmt.Errorf("not JSON: %.200s", text)
}
