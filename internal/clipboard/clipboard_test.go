package clipboard

import (
	"bytes"
	"testing"
)

var png = []byte("\x89PNG\r\n\x1a\nIHDR")

// The signature is checked rather than the exit code: xclip prints its
// complaints to stdout, so a tool that "succeeded" can still have handed back
// an error message where the image should be.
func TestIsPNGRejectsWhatIsNotOne(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []byte
		want bool
	}{
		{"a png", png, true},
		{"empty", nil, false},
		{"an error message", []byte("Error: target image/png not available"), false},
		{"a jpeg", []byte("\xff\xd8\xff\xe0\x00\x10JFIF"), false},
		{"a truncated header", []byte("\x89PNG"), false},
	} {
		if got := isPNG(tc.in); got != tc.want {
			t.Errorf("%s: isPNG = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestDecodeHexReadsAppleScriptData(t *testing.T) {
	// osascript prints raw data with spaces and newlines through it.
	got, err := decodeHex("89 50 4E 47\n0D 0A 1A 0A")
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte("\x89PNG\r\n\x1a\n"); !bytes.Equal(got, want) {
		t.Errorf("decoded %q, want %q", got, want)
	}
	if _, err := decodeHex("89 5"); err == nil {
		t.Error("an odd number of digits should not decode")
	}
}

func TestSizeReadsAsASize(t *testing.T) {
	for _, tc := range []struct {
		in   int
		want string
	}{
		{512, "512B"}, {2048, "2KB"}, {5 << 20, "5.0MB"},
	} {
		if got := size(tc.in); got != tc.want {
			t.Errorf("size(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
