package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// typing feeds a string to the prompt one key at a time and returns the last
// command, which is what a completion lookup arrives as.
func typing(t *testing.T, m *Model, s string) tea.Cmd {
	t.Helper()
	var cmd tea.Cmd
	for _, r := range s {
		_, cmd = m.Update(key(string(r)))
	}
	return cmd
}

// Typing @ opens the file menu on the spot. It is a gesture borrowed from chat
// clients, where the list appears with the key; a marker that did nothing until
// you also pressed Tab would just be a worse Tab.
func TestRefOpensTheMenuAsItIsTyped(t *testing.T) {
	m := newTestModel(t)
	cmd := typing(t, m, "xem @")

	msg := runUntil[completionMsg](t, cmd)
	if !msg.auto {
		t.Error("the lookup should be marked as one nobody asked for")
	}
	m.Update(msg)

	if !m.compOpen {
		t.Fatalf("no menu opened for @; candidates: %d", len(m.comp.Candidates))
	}
	if got := m.input.Value(); got != "xem @" {
		t.Errorf("opening the menu changed the prompt to %q", got)
	}
}

// A menu that opened by itself may offer but not type: the caret belongs to
// whoever is still typing the word.
func TestRefMenuNeverCompletesOnItsOwn(t *testing.T) {
	m := newTestModel(t)
	// "main.go" is the only thing in the fixture starting with "mai", so an
	// asked-for completion would finish the word here.
	cmd := typing(t, m, "xem @mai")

	msg := runUntil[completionMsg](t, cmd)
	m.Update(msg)

	if got := m.input.Value(); got != "xem @mai" {
		t.Errorf("the prompt became %q on its own", got)
	}
}

// Tab still finishes the word, marker and all, so the two ways of completing a
// reference agree on what a reference is.
func TestTabCompletesAReference(t *testing.T) {
	m := newTestModel(t)
	typing(t, m, "xem @mai")

	msg := runUntil[completionMsg](t, m.completeCmd(false))
	m.Update(msg)

	if got := m.input.Value(); !strings.HasPrefix(got, "xem @main.go") {
		t.Errorf("tab left the prompt at %q, want the reference completed", got)
	}
}

// Nothing changes for an ordinary word: the menu is not meant to follow you
// around the prompt.
func TestPlainWordDoesNotOpenAMenu(t *testing.T) {
	m := newTestModel(t)
	typing(t, m, "xem main")
	if m.compOpen {
		t.Error("a plain word opened a menu")
	}
}
