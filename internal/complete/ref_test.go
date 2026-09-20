package complete

import (
	"context"
	"slices"
	"testing"
)

// A reference completes as a path with its marker kept: what goes back into the
// prompt has to still be a reference, or the second Tab would complete a
// different word than the first.
func TestRefCompletesLikeAPath(t *testing.T) {
	fs, root := fixture(t)

	plain := Paths(context.Background(), fs, root, "", Token{Text: "inte", End: 4}, Anything)
	ref := Paths(context.Background(), fs, root, "", Token{Text: "@inte", End: 5}, Anything)

	if len(ref.Candidates) != len(plain.Candidates) || len(ref.Candidates) == 0 {
		t.Fatalf("@inte offered %v, inte offered %v", inserts(ref), inserts(plain))
	}
	for i, c := range ref.Candidates {
		if want := Ref + plain.Candidates[i].Insert; c.Insert != want {
			t.Errorf("candidate %d is %q, want %q", i, c.Insert, want)
		}
		// The menu shows the file, not the syntax that pointed at it.
		if c.Display != plain.Candidates[i].Display {
			t.Errorf("candidate %d displays %q, want %q", i, c.Display, plain.Candidates[i].Display)
		}
	}
	if ref.Common != Ref+plain.Common {
		t.Errorf("common prefix is %q, want %q", ref.Common, Ref+plain.Common)
	}
	// Extends compares against the token, which carries the marker too, so a
	// reference that has nothing left to add does not claim otherwise.
	if !ref.Extends() {
		t.Errorf("@inte should extend to %q", ref.Common)
	}
}

// Completing a directory inside a reference keeps going, the same as a path.
func TestRefDescendsIntoDirectories(t *testing.T) {
	fs, root := fixture(t)
	res := Paths(context.Background(), fs, root, "", Token{Text: "@internal/", End: 10}, Anything)
	if got := inserts(res); !slices.Contains(got, "@internal/ui/") {
		t.Errorf("completing @internal/ gave %v", got)
	}
}

func TestTrimRef(t *testing.T) {
	for _, tc := range []struct{ in, path, marker string }{
		{"@internal/ui", "internal/ui", "@"},
		{"@", "", "@"},
		{"internal/ui", "internal/ui", ""},
		{"", "", ""},
		{"a@b", "a@b", ""},
	} {
		path, marker := TrimRef(tc.in)
		if path != tc.path || marker != tc.marker {
			t.Errorf("TrimRef(%q) = %q, %q; want %q, %q", tc.in, path, marker, tc.path, tc.marker)
		}
	}
}
