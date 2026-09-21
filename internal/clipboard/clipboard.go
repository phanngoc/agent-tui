// Package clipboard reads an image out of the system clipboard.
//
// A terminal cannot paste one. Bracketed paste carries text, and an image on
// the clipboard arrives as nothing at all — which is why pressing the paste key
// over a screenshot does nothing anywhere in a terminal. The only way to get it
// is to ask the operating system directly, so that is what this does: one
// helper per platform, each handing back PNG bytes.
//
// Every backend writes to its stdout rather than to a file we name. Under WSL
// that is the difference between working and not: the helper that can reach the
// Windows clipboard is a Windows process, and a path it can write to is not one
// this process can read.
package clipboard

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// ErrNoImage means the clipboard was readable and held no image. It is the
// ordinary outcome of pasting when you have copied text, so the UI says so
// plainly instead of reporting a failure.
var ErrNoImage = errors.New("no image on the clipboard")

// ErrNoTool means nothing on this machine can read the clipboard. The message
// names what to install, because that is the only way out of it.
type ErrNoTool struct{ Hint string }

func (e *ErrNoTool) Error() string { return e.Hint }

// MaxBytes caps what will be accepted. The API rejects an oversized image
// anyway, and a 40MB screenshot is never what someone meant to send.
const MaxBytes = 5 << 20

// Image returns the clipboard's image as PNG bytes.
func Image(ctx context.Context) ([]byte, error) {
	sources := backends()
	if len(sources) == 0 {
		return nil, &ErrNoTool{Hint: "no clipboard helper for " + runtime.GOOS}
	}

	var missing []string
	empty := false
	for _, s := range sources {
		if _, err := exec.LookPath(s.bin); err != nil {
			missing = append(missing, s.bin)
			continue
		}
		data, err := s.read(ctx)
		switch {
		case errors.Is(err, ErrNoImage):
			// The tool works and the clipboard holds no image. Keep trying the
			// others — under WSL the Linux and the Windows clipboard are two
			// different clipboards, and the image is usually in the other one.
			empty = true
		case err != nil:
			continue
		case len(data) > MaxBytes:
			return nil, fmt.Errorf("image is %s, over the %s limit",
				size(len(data)), size(MaxBytes))
		case len(data) > 0:
			return data, nil
		default:
			empty = true
		}
	}
	if empty {
		return nil, ErrNoImage
	}
	return nil, &ErrNoTool{Hint: "install " + strings.Join(missing, " or ") + " to paste images"}
}

type backend struct {
	bin  string
	read func(context.Context) ([]byte, error)
}

func backends() []backend {
	switch runtime.GOOS {
	case "windows":
		return []backend{{bin: "powershell.exe", read: fromWindows}}
	case "darwin":
		return []backend{
			{bin: "pngpaste", read: shellOut("pngpaste", "-")},
			{bin: "osascript", read: fromMac},
		}
	default:
		// Wayland first, then X11, then the Windows clipboard: a Linux binary
		// running under WSL reaches it through interop, and that is where an
		// image copied from a Windows app actually is.
		return []backend{
			{bin: "wl-paste", read: shellOut("wl-paste", "--no-newline", "--type", "image/png")},
			{bin: "xclip", read: shellOut("xclip", "-selection", "clipboard", "-target", "image/png", "-out")},
			{bin: "powershell.exe", read: fromWindows},
		}
	}
}

// shellOut runs a tool that writes the PNG to its stdout.
func shellOut(name string, args ...string) func(context.Context) ([]byte, error) {
	return func(ctx context.Context) ([]byte, error) {
		var out, errb bytes.Buffer
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			// Both tools fail rather than return nothing when the clipboard
			// holds no image of that type, which is not an error worth showing.
			if isEmptyClipboard(errb.String()) {
				return nil, ErrNoImage
			}
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if !isPNG(out.Bytes()) {
			return nil, ErrNoImage
		}
		return out.Bytes(), nil
	}
}

func isEmptyClipboard(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "no suitable type") ||
		strings.Contains(s, "target image/png not available") ||
		strings.Contains(s, "no selection")
}

// winScript asks the Windows clipboard for a bitmap, and failing that for a
// copied image file, and prints it as base64. Base64 rather than raw bytes
// because the output crosses a PowerShell pipeline that would otherwise
// re-encode it, and a path because a screenshot and a copied file are the two
// ways an image reaches the clipboard.
const winScript = `
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
function Emit($img) {
  $ms = New-Object System.IO.MemoryStream
  $img.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png)
  [Console]::Out.Write([Convert]::ToBase64String($ms.ToArray()))
}
$img = [System.Windows.Forms.Clipboard]::GetImage()
if ($img -ne $null) { Emit $img; exit 0 }
foreach ($f in [System.Windows.Forms.Clipboard]::GetFileDropList()) {
  if ($f -match '\.(png|jpe?g|gif|webp|bmp)$') {
    $src = [System.Drawing.Image]::FromFile($f)
    Emit $src
    $src.Dispose()
    exit 0
  }
}
exit 3
`

// fromWindows reads the Windows clipboard, including from inside WSL.
func fromWindows(ctx context.Context) ([]byte, error) {
	// -STA because the clipboard API is single-threaded-apartment only, and
	// -Command - because a script on stdin needs no quoting to survive.
	cmd := exec.CommandContext(ctx, "powershell.exe",
		"-NoProfile", "-NonInteractive", "-STA", "-Command", "-")
	cmd.Stdin = strings.NewReader(winScript)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()

	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 3 {
		return nil, ErrNoImage
	}
	if err != nil {
		return nil, fmt.Errorf("powershell: %w: %s", err, firstLine(errb.String()))
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(out.String()))
	if err != nil {
		return nil, fmt.Errorf("powershell returned %d bytes that are not base64", out.Len())
	}
	if !isPNG(data) {
		return nil, ErrNoImage
	}
	return data, nil
}

// macScript is the fallback for a Mac without pngpaste: AppleScript can pull
// the clipboard's PNG out as a hex literal, which is ugly but always present.
const macScript = `try
	set png to (the clipboard as «class PNGf»)
on error
	return ""
end try
return png`

func fromMac(ctx context.Context) ([]byte, error) {
	out, err := exec.CommandContext(ctx, "osascript", "-e", macScript).Output()
	if err != nil {
		return nil, fmt.Errorf("osascript: %w", err)
	}
	// osascript prints raw data as «data PNGf89504E47...».
	s := strings.TrimSpace(string(out))
	i, j := strings.Index(s, "PNGf"), strings.LastIndex(s, "»")
	if i < 0 || j <= i {
		return nil, ErrNoImage
	}
	data, err := decodeHex(s[i+4 : j])
	if err != nil || !isPNG(data) {
		return nil, ErrNoImage
	}
	return data, nil
}

func decodeHex(s string) ([]byte, error) {
	out := make([]byte, 0, len(s)/2)
	var hi byte
	half := false
	for i := 0; i < len(s); i++ {
		var v byte
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			v = c - '0'
		case c >= 'a' && c <= 'f':
			v = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			v = c - 'A' + 10
		default:
			continue
		}
		if half {
			out = append(out, hi<<4|v)
			half = false
			continue
		}
		hi, half = v, true
	}
	if half {
		return nil, errors.New("odd number of hex digits")
	}
	return out, nil
}

// isPNG checks the signature rather than trusting the tool: xclip happily
// prints an error page to stdout when the target is not available.
func isPNG(b []byte) bool {
	return len(b) > 8 && string(b[:8]) == "\x89PNG\r\n\x1a\n"
}

func size(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%dKB", n>>10)
	default:
		return fmt.Sprintf("%dB", n)
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
