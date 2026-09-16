package agent

import (
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// The models the picker offers, and the two things about each one that change
// the shape of a request.
//
// Reasoning is not configured the same way across generations: the current
// models take adaptive thinking and an effort level, while Haiku 4.5 predates
// both and takes a fixed token budget instead — sending it an effort rejects
// the whole request. A picker that offered it without knowing that would hand
// the user a model that fails on the first prompt, so what the request may
// contain is a property of the model here rather than a constant in the agent.
//
// The notes describe cost in relative terms on purpose. Prices change, and a
// picker confidently quoting a stale dollar figure is worse than one that says
// which model is the cheap one.

// Model is one model, and what its requests may carry.
type Model struct {
	ID    string
	Label string
	Note  string
	// Effort reports whether the model accepts output_config.effort.
	Effort bool
	// Adaptive reports whether reasoning is configured with adaptive thinking.
	// A model without it takes a fixed thinking budget.
	Adaptive bool
}

// Models is the catalogue, most useful first. The head of the list is the
// default a new install gets.
var Models = []Model{
	{
		ID: "claude-opus-5", Label: "Opus 5",
		Note:   "1M context · the default, and the strongest all-round coding model",
		Effort: true, Adaptive: true,
	},
	{
		ID: "claude-fable-5-1", Label: "Fable 5.1",
		Note:   "1M context · the most capable, and the most expensive",
		Effort: true, Adaptive: true,
	},
	{
		ID: "claude-sonnet-5", Label: "Sonnet 5",
		Note:   "1M context · faster and cheaper than Opus",
		Effort: true, Adaptive: true,
	},
	{
		ID: "claude-haiku-4-5", Label: "Haiku 4.5",
		Note:   "200K context · the cheapest; thinks to a fixed budget, no effort setting",
		Effort: false, Adaptive: false,
	},
}

// DefaultModel is what a session runs on when nothing has chosen otherwise.
const DefaultModel = "claude-opus-5"

// ModelFor describes a model id, including one that is not in the catalogue.
//
// An unknown id is assumed to take the current request shape rather than being
// rejected: a config file may name a model released after this build, and
// refusing to run it would be worse than trying. A model that turns out not to
// accept those fields says so in the API error, which the transcript shows.
func ModelFor(id string) Model {
	for _, m := range Models {
		if m.ID == id {
			return m
		}
	}
	return Model{ID: id, Label: id, Effort: true, Adaptive: true}
}

// ResolveModel maps what a user typed onto a model.
//
// A bare family name means the current generation of it, because that is what
// someone who has not been following version numbers means by "sonnet". Both
// the id and the label are accepted, with or without their punctuation, so
// "opus 5", "Opus-5" and "claude-opus-5" are the same request.
func ResolveModel(name string) (Model, bool) {
	want := foldModel(name)
	if want == "" {
		return Model{}, false
	}
	switch want {
	case "opus":
		return ModelFor("claude-opus-5"), true
	case "sonnet":
		return ModelFor("claude-sonnet-5"), true
	case "haiku":
		return ModelFor("claude-haiku-4-5"), true
	case "fable":
		return ModelFor("claude-fable-5-1"), true
	}
	for _, m := range Models {
		if foldModel(m.ID) == want || foldModel(m.Label) == want {
			return m, true
		}
	}
	// A full id that is not in the catalogue is still a model, and naming one
	// explicitly is a deliberate act rather than a typo worth refusing.
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), "claude-") {
		return ModelFor(strings.TrimSpace(name)), true
	}
	return Model{}, false
}

// thinkingFor asks a model to reason in whichever way it accepts.
//
// Adaptive lets the model decide how much to think; a model without it needs a
// budget named up front, which the API requires to be at least 1024 tokens and
// strictly below the response cap.
func thinkingFor(m Model, maxTokens int64) anthropic.ThinkingConfigParamUnion {
	if m.Adaptive {
		return anthropic.ThinkingConfigParamUnion{
			OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{
				Display: anthropic.ThinkingConfigAdaptiveDisplaySummarized,
			},
		}
	}
	budget := maxTokens / 2
	if budget < 1024 {
		budget = 1024
	}
	if budget >= maxTokens {
		// No room for both a floor of 1024 and a response: skip thinking
		// rather than send a request the API will refuse.
		return anthropic.ThinkingConfigParamUnion{
			OfDisabled: &anthropic.ThinkingConfigDisabledParam{},
		}
	}
	return anthropic.ThinkingConfigParamOfEnabled(budget)
}

// foldModel reduces a name to its letters and digits, so the separators nobody
// remembers — "4-5", "4.5", "45" — stop being three different things.
func foldModel(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return strings.TrimPrefix(b.String(), "claude")
}
