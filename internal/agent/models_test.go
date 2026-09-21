package agent

import (
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

func TestCatalogueIsWellFormed(t *testing.T) {
	if len(Models) == 0 {
		t.Fatal("the picker would be empty")
	}
	if Models[0].ID != DefaultModel {
		t.Errorf("the catalogue opens with %q but the default is %q", Models[0].ID, DefaultModel)
	}
	seen := map[string]bool{}
	for _, m := range Models {
		if seen[m.ID] {
			t.Errorf("%q is listed twice", m.ID)
		}
		seen[m.ID] = true
		if !strings.HasPrefix(m.ID, "claude-") {
			t.Errorf("%q does not look like a model id", m.ID)
		}
		if m.Label == "" || m.Note == "" {
			t.Errorf("%q has nothing for the picker to show", m.ID)
		}
		// A date suffix is the classic way a model id goes stale; the current
		// ids carry none.
		if n := strings.Count(m.ID, "-"); strings.HasSuffix(m.ID, "0") && n > 3 {
			t.Errorf("%q looks like a dated snapshot", m.ID)
		}
	}
}

func TestResolveModel(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// Bare family names mean the current generation of that family.
		{"opus", "claude-opus-5"},
		{"sonnet", "claude-sonnet-5"},
		{"haiku", "claude-haiku-4-5"},
		{"fable", "claude-fable-5-1"},
		{"OPUS", "claude-opus-5"},
		// Labels, ids, and the separators nobody remembers.
		{"Opus 5", "claude-opus-5"},
		{"opus-5", "claude-opus-5"},
		{"claude-opus-5", "claude-opus-5"},
		{"Haiku 4.5", "claude-haiku-4-5"},
		{"haiku45", "claude-haiku-4-5"},
		{"Fable 5.1", "claude-fable-5-1"},
		// A model released after this build is still a model.
		{"claude-opus-9", "claude-opus-9"},
	}
	for _, c := range cases {
		got, ok := ResolveModel(c.in)
		if !ok {
			t.Errorf("ResolveModel(%q) failed", c.in)
			continue
		}
		if got.ID != c.want {
			t.Errorf("ResolveModel(%q) = %q, want %q", c.in, got.ID, c.want)
		}
	}
	for _, bad := range []string{"", "   ", "gpt-5", "llama", "opusss"} {
		if got, ok := ResolveModel(bad); ok {
			t.Errorf("ResolveModel(%q) resolved to %q", bad, got.ID)
		}
	}
}

// TestHaikuTakesNoEffort is the reason the catalogue carries capabilities at
// all: sending an effort to a model that has none fails the whole request, so
// a picker that offered it blindly would hand the user a model that dies on
// the first prompt.
func TestHaikuTakesNoEffort(t *testing.T) {
	h := ModelFor("claude-haiku-4-5")
	if h.Effort {
		t.Error("Haiku 4.5 is marked as taking an effort level")
	}
	if h.Adaptive {
		t.Error("Haiku 4.5 is marked as taking adaptive thinking")
	}
	for _, id := range []string{"claude-opus-5", "claude-sonnet-5", "claude-fable-5-1"} {
		m := ModelFor(id)
		if !m.Effort || !m.Adaptive {
			t.Errorf("%s should take both effort and adaptive thinking", id)
		}
	}
}

func TestModelForUnknownAssumesTheCurrentShape(t *testing.T) {
	m := ModelFor("claude-something-new")
	if m.ID != "claude-something-new" {
		t.Errorf("id = %q", m.ID)
	}
	if !m.Effort || !m.Adaptive {
		t.Error("an unknown model should be tried with the current request shape")
	}
}

func TestThinkingForAdaptive(t *testing.T) {
	got := thinkingFor(ModelFor("claude-opus-5"), 32000)
	if got.OfAdaptive == nil {
		t.Fatal("an adaptive model did not get adaptive thinking")
	}
	if got.OfAdaptive.Display != anthropic.ThinkingConfigAdaptiveDisplaySummarized {
		t.Error("reasoning would not be shown in the transcript")
	}
}

func TestThinkingForBudgeted(t *testing.T) {
	h := ModelFor("claude-haiku-4-5")

	got := thinkingFor(h, 32000)
	if got.OfEnabled == nil {
		t.Fatal("a budgeted model did not get a thinking budget")
	}
	// The API requires a budget of at least 1024 and strictly below max_tokens.
	if b := got.OfEnabled.BudgetTokens; b < 1024 || b >= 32000 {
		t.Errorf("budget %d is outside what the API accepts", b)
	}

	// A cap too small to hold both a floor of 1024 and a reply must not
	// produce a request the API would refuse.
	tight := thinkingFor(h, 1024)
	if tight.OfEnabled != nil && tight.OfEnabled.BudgetTokens >= 1024 {
		t.Errorf("budget %d is not below the 1024-token cap", tight.OfEnabled.BudgetTokens)
	}
}
