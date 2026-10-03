package clipboard

import (
	"context"
	"encoding/base64"
	"os/exec"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestEncodePSIsUTF16Base64(t *testing.T) {
	b, err := base64.StdEncoding.DecodeString(encodePS("Ảnh ✓"))
	if err != nil || len(b)%2 != 0 {
		t.Fatalf("not base64 of UTF-16: %v", err)
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	if got := string(utf16.Decode(u)); got != "Ảnh ✓" {
		t.Errorf("round trip gave %q", got)
	}
}

// TestMultiLineScriptsRun is the bug: fed on stdin with -Command -, a script
// with a function in it ran nothing and exited 0, so every clipboard image
// read as "no image on the clipboard". The script below has the same shape
// as the one that reads images.
func TestMultiLineScriptsRun(t *testing.T) {
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		t.Skip("no Windows PowerShell here")
	}
	script := "$ErrorActionPreference = 'Stop'\n" +
		"function Emit($s) {\n" +
		"  [Console]::Out.Write($s)\n" +
		"}\n" +
		"if ($true) { Emit 'ran' ; exit 0 }\n" +
		"exit 3\n"
	out, err := psCommand(context.Background(), script).Output()
	if err != nil || strings.TrimSpace(string(out)) != "ran" {
		t.Errorf("out=%q err=%v", out, err)
	}
}
