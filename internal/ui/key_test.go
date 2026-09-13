package ui

import "testing"

// TestKeyHelperRoundTrips guards the test helper itself: it silently built the
// wrong key for alt+ and shift+, so a binding could look untested and pass.
func TestKeyHelperRoundTrips(t *testing.T) {
	for _, name := range []string{
		"a", "ctrl+t", "alt+t", "shift+g", "enter", "esc", "tab", "up", "down", "f1",
		// A modifier on a named key is the case that slipped through: this
		// used to build shift on the letter t instead of shift on tab.
		"shift+tab", "ctrl+left", "alt+down", "left", "right", "home", "end",
	} {
		if got := key(name).String(); got != name {
			t.Errorf("key(%q) builds %q", name, got)
		}
	}
}
