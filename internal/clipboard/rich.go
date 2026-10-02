package clipboard

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Rich text: the same thing as HTML and as plain text, at once.
//
// A clipboard holds one item in several formats, and the program pasting
// picks the best it understands. Slack's composer takes the HTML and turns it
// into its own formatting; a terminal or a text editor takes the text. Writing
// both is what lets one copy serve each.
//
// Only the platforms whose clipboard can be given both formats from a script
// get the HTML: Windows (and WSL, which pastes into Windows apps) and macOS.
// The Linux tools set one format per call, and setting HTML alone would leave
// every plain-text paste empty, so there the text is all that is written.

// WriteRich puts html and its plain-text equivalent on the clipboard together.
// html is a fragment: no <html> or <body>.
func WriteRich(ctx context.Context, html, text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var rich func(context.Context, string, string) error
	switch {
	case runtime.GOOS == "windows":
		rich = richWindows("powershell")
	case runtime.GOOS == "darwin":
		rich = richMac
	case isWSL():
		rich = richWindows("powershell.exe")
	}
	if rich != nil {
		err := rich(ctx, html, text)
		if err == nil || ctx.Err() != nil {
			return err
		}
	}
	return Write(ctx, text)
}

func isWSL() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	b, err := os.ReadFile("/proc/version")
	return err == nil && bytes.Contains(bytes.ToLower(b), []byte("microsoft"))
}

// cfHTML wraps a fragment in the header Windows' "HTML Format" requires: byte
// offsets of the document and of the fragment inside it, each padded to a
// fixed width so the header's own length does not depend on them.
func cfHTML(fragment string) []byte {
	const header = "Version:0.9\r\nStartHTML:%010d\r\nEndHTML:%010d\r\n" +
		"StartFragment:%010d\r\nEndFragment:%010d\r\n"
	pre := "<html><head><meta charset=\"utf-8\"></head><body>\r\n<!--StartFragment-->"
	post := "<!--EndFragment-->\r\n</body></html>"

	hlen := len(fmt.Sprintf(header, 0, 0, 0, 0))
	startHTML := hlen
	startFrag := startHTML + len(pre)
	endFrag := startFrag + len(fragment)
	endHTML := endFrag + len(post)
	return []byte(fmt.Sprintf(header, startHTML, endHTML, startFrag, endFrag) + pre + fragment + post)
}

// winRich sets both formats in one clipboard item. Both arrive base64 encoded
// on stdin, one per line, for the same reason Write does it: the console in
// between is not UTF-8. The HTML goes in as a stream of exact bytes, because
// the offsets in its header count bytes and any re-encoding would break them.
const winRich = `
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms
$in = [Console]::In.ReadToEnd().Split([char]10)
$html = [Convert]::FromBase64String($in[0].Trim())
$txt = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($in[1].Trim()))
$d = New-Object System.Windows.Forms.DataObject
$d.SetData('HTML Format', (New-Object IO.MemoryStream(,$html)))
$d.SetData([System.Windows.Forms.DataFormats]::UnicodeText, $txt)
[System.Windows.Forms.Clipboard]::SetDataObject($d, $true)
`

func richWindows(name string) func(context.Context, string, string) error {
	return func(ctx context.Context, html, text string) error {
		cmd := exec.CommandContext(ctx, name, "-NoProfile", "-NonInteractive",
			"-STA", "-Command", winRich)
		cmd.Stdin = strings.NewReader(
			base64.StdEncoding.EncodeToString(cfHTML(html)) + "\n" +
				base64.StdEncoding.EncodeToString([]byte(text)) + "\n")
		return runQuiet(cmd)
	}
}

// richMac hands AppleScript both formats as hex data, which needs no quoting
// and survives any character in either.
func richMac(ctx context.Context, html, text string) error {
	doc := "<html><head><meta charset=\"utf-8\"></head><body>" + html + "</body></html>"
	script := fmt.Sprintf(`set the clipboard to {«class HTML»:«data HTML%s», «class utf8»:«data utf8%s»}`,
		strings.ToUpper(hex.EncodeToString([]byte(doc))),
		strings.ToUpper(hex.EncodeToString([]byte(text))))
	return runQuiet(exec.CommandContext(ctx, "osascript", "-e", script))
}

func runQuiet(cmd *exec.Cmd) error {
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
