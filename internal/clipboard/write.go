package clipboard

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os/exec"
	"runtime"
	"strings"
)

// Writing is the other half, and it is the half a terminal cannot do either.
//
// Selecting text with the mouse is the terminal's own gesture, and it stops
// being available the moment a program asks for mouse reporting: the drag
// becomes ours, and the terminal has nothing left to select with. A program
// that takes the mouse therefore owes the user a selection of its own, and a
// selection is worth nothing if it cannot be put on the clipboard.
//
// So: one helper per platform again, each taking the text on stdin. The
// Windows one takes it base64-encoded because stdin crosses a console whose
// code page is not UTF-8 — the same reason the image side sends base64 the
// other way — and Vietnamese text is exactly what that mangles.

// Write puts text on the system clipboard.
func Write(ctx context.Context, text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var last error
	for _, b := range writers() {
		switch err := b.write(ctx, text); {
		case err == nil:
			return nil
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(err, exec.ErrNotFound):
			continue // this machine does not have that one
		default:
			last = err
		}
	}
	if last != nil {
		return last
	}
	return &ErrNoTool{Hint: writeHint()}
}

type writer struct {
	name  string
	write func(context.Context, string) error
}

func writers() []writer {
	switch runtime.GOOS {
	case "windows":
		return []writer{{"powershell", toWindows}}
	case "darwin":
		return []writer{{"pbcopy", pipeTo("pbcopy")}}
	default:
		// A WSL session has a Windows clipboard behind it and usually no X
		// server in front of it, so the Windows helper is tried too — last,
		// because a Linux desktop that has both wants its own.
		return []writer{
			{"wl-copy", pipeTo("wl-copy")},
			{"xclip", pipeTo("xclip", "-selection", "clipboard")},
			{"xsel", pipeTo("xsel", "--clipboard", "--input")},
			{"powershell.exe", toWindows},
		}
	}
}

func writeHint() string {
	if runtime.GOOS == "linux" {
		return "no clipboard tool found — install wl-clipboard or xclip"
	}
	return "no clipboard tool found"
}

// pipeTo hands the text to a tool on stdin, which is what every Unix clipboard
// tool wants and what none of them can get wrong: their stdin is bytes, and the
// bytes are already UTF-8.
func pipeTo(name string, args ...string) func(context.Context, string) error {
	return func(ctx context.Context, text string) error {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Stdin = strings.NewReader(text)
		var errBuf bytes.Buffer
		cmd.Stderr = &errBuf
		if err := cmd.Run(); err != nil {
			if errors.Is(err, exec.ErrNotFound) {
				return err
			}
			return errors.New(strings.TrimSpace(firstLine(errBuf.String()) + " " + err.Error()))
		}
		return nil
	}
}

// winWrite decodes base64 back to UTF-8 inside PowerShell and sets the
// clipboard from it. Base64 is ASCII, so nothing between here and there can
// reinterpret it: the console code page, the pipe and the argument parser all
// leave it alone, and the text comes out the far side as it went in.
const winWrite = `
$ErrorActionPreference = 'Stop'
$b64 = [Console]::In.ReadToEnd()
$txt = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($b64))
Set-Clipboard -Value $txt
`

func toWindows(ctx context.Context, text string) error {
	name := "powershell.exe"
	if runtime.GOOS == "windows" {
		name = "powershell"
	}
	cmd := exec.CommandContext(ctx, name, "-NoProfile", "-NonInteractive",
		"-STA", "-Command", winWrite)
	cmd.Stdin = strings.NewReader(base64.StdEncoding.EncodeToString([]byte(text)))
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return err
		}
		if msg := firstLine(errBuf.String()); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}
