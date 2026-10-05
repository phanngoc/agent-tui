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

// DefaultModel does the extracting. Learning runs in the background, so speed
// matters less than judgement: deciding what is worth remembering, merging it
// with what is already known and writing scenes and a persona that read well
// are where a small model's answers showed — vague memories, merges missed,
// JSON that would not parse. Sonnet is the balance of quality and cost; the
// setting picks another.
const DefaultModel = "claude-sonnet-5-5"

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
	ctx, cancel := context.WithTimeout(ctx, 8*time.Minute)
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
		// The CLI reports most failures on stdout, in its JSON, not on stderr.
		why := strings.TrimSpace(errb.String())
		var res struct {
			Result string `json:"result"`
		}
		if json.Unmarshal(out.Bytes(), &res) == nil && res.Result != "" {
			why = strings.TrimSpace(why + " " + res.Result)
		} else if why == "" {
			why = strings.TrimSpace(out.String())
		}
		if len(why) > 300 {
			why = why[:300] + "…"
		}
		return "", fmt.Errorf("claude: %v: %s", err, why)
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
	var first error
	try := func(s string) bool {
		err := json.Unmarshal([]byte(s), v)
		if err != nil && first == nil {
			first = err
		}
		return err == nil
	}
	// Models write JSON a strict parser refuses in a few ways that are easy
	// to put right: a raw line break inside a string (a Markdown body, most
	// often), prose around the JSON, an answer cut off before it closed.
	for _, cand := range []string{text, escapeControls(text)} {
		if try(cand) {
			return nil
		}
		for _, pair := range [][2]string{{"[", "]"}, {"{", "}"}} {
			i, j := strings.Index(cand, pair[0]), strings.LastIndex(cand, pair[1])
			if i >= 0 && j > i && try(cand[i:j+1]) {
				return nil
			}
		}
		if i := strings.IndexAny(cand, "[{"); i >= 0 && try(closeJSON(cand[i:])) {
			return nil
		}
	}
	return fmt.Errorf("not JSON (%v): %.200s", first, text)
}

// escapeControls escapes the control characters JSON forbids inside strings.
func escapeControls(s string) string {
	var b strings.Builder
	in, esc := false, false
	for _, r := range s {
		switch {
		case esc:
			esc = false
		case in && r == '\\':
			esc = true
		case r == '"':
			in = !in
		case in && r == '\n':
			b.WriteString(`\n`)
			continue
		case in && r == '\r':
			continue
		case in && r == '\t':
			b.WriteString(`\t`)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// closeJSON finishes an answer cut off mid-way: it closes an open string,
// drops a dangling comma or key, and closes every open bracket.
func closeJSON(s string) string {
	var stack []rune
	in, esc := false, false
	for _, r := range s {
		switch {
		case esc:
			esc = false
		case in && r == '\\':
			esc = true
		case r == '"':
			in = !in
		case in:
		case r == '{' || r == '[':
			stack = append(stack, r)
		case (r == '}' || r == ']') && len(stack) > 0:
			stack = stack[:len(stack)-1]
		}
	}
	if in {
		s += `"`
	}
	s = strings.TrimRight(s, " \n\r\t")
	s = strings.TrimSuffix(s, ",")
	if strings.HasSuffix(s, ":") {
		s += "null"
	}
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == '{' {
			s += "}"
		} else {
			s += "]"
		}
	}
	return s
}

// flexInt reads a number however a model wrote it: 80, 80.0, "80", or ""
// for none.
type flexInt struct {
	V   int
	Set bool
}

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		return nil
	}
	var n float64
	if err := json.Unmarshal([]byte(s), &n); err != nil {
		return nil // not a number: as good as absent
	}
	f.V, f.Set = int(n), true
	return nil
}

// flexStrings reads a list of strings, accepting a lone string or "" too.
type flexStrings []string

func (f *flexStrings) UnmarshalJSON(b []byte) error {
	var list []string
	if json.Unmarshal(b, &list) == nil {
		*f = list
		return nil
	}
	var one string
	if json.Unmarshal(b, &one) == nil && one != "" {
		*f = []string{one}
	}
	return nil
}
