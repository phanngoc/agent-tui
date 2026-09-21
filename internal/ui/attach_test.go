package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// stagePNG puts a real file where a staged attachment would be, so the tests
// exercise the same path the clipboard produces.
func stagePNG(t *testing.T, m *Model, bytes int) session.Attachment {
	t.Helper()
	path := filepath.Join(t.TempDir(), "paste-1.png")
	data := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, bytes)...)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	att := session.Attachment{Path: path, Media: "image/png", Bytes: int64(len(data))}
	m.onPasted(pastedMsg{att: att})
	return att
}

// A staged image is announced in the prompt and in the box around it. An
// attachment nobody can see is one that gets sent by accident.
func TestPastedImageIsVisibleBeforeItIsSent(t *testing.T) {
	m := newTestModel(t)
	m.input.SetValue("cái này")
	stagePNG(t, m, 2048)

	if got := m.input.Value(); !strings.Contains(got, "[image #1]") {
		t.Errorf("the prompt should mark where the image went, got %q", got)
	}
	if m.attachRows() == 0 {
		t.Error("the prompt box should have made room for the attachment bar")
	}
	if bar := stripANSI(m.attachBar()); !strings.Contains(bar, "[image #1]") {
		t.Errorf("the attachment bar shows %q", bar)
	}
}

// The image rides on the message and on the turn: the transcript needs it to
// show what was sent, and the engine needs it to send anything at all.
func TestSendCarriesTheStagedImage(t *testing.T) {
	m := newTestModel(t)
	att := stagePNG(t, m, 512)

	m.send("xem ảnh này")

	s := m.mgr.Active()
	last := s.Last()
	if last == nil || len(last.Files) != 1 {
		t.Fatalf("the message went out without its image: %+v", last)
	}
	if last.Files[0].Path != att.Path {
		t.Errorf("attached %q, want %q", last.Files[0].Path, att.Path)
	}
	if len(m.attach) != 0 {
		t.Error("the staging area should be empty once the message is sent")
	}
	if m.attachRows() != 0 {
		t.Error("the prompt box should have given its extra row back")
	}
}

// Clearing the prompt clears what was attached to it: the markers that pointed
// at the images are gone, so keeping them staged would send pictures that
// nothing in the text refers to.
func TestClearingThePromptDropsAttachments(t *testing.T) {
	m := newTestModel(t)
	stagePNG(t, m, 512)

	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})

	if len(m.attach) != 0 {
		t.Errorf("ctrl+u left %d attachments staged", len(m.attach))
	}
	if m.input.Value() != "" {
		t.Errorf("ctrl+u left %q in the prompt", m.input.Value())
	}
}

func TestTranscriptShowsAttachedImages(t *testing.T) {
	m := newTestModel(t)
	stagePNG(t, m, 4096)
	m.send("xem ảnh này")

	out := stripANSI(m.transcript(80))
	if !strings.Contains(out, "[image #1]") {
		t.Errorf("the transcript should show what was attached:\n%s", out)
	}
}

// A failed read says why. Pasting with text on the clipboard is the common
// case, and it has to read as an answer rather than as a malfunction.
func TestPasteFailureIsReported(t *testing.T) {
	m := newTestModel(t)
	m.onPasted(pastedMsg{err: errNoImage{}})

	if m.errText == "" {
		t.Error("a failed paste should say something")
	}
	if len(m.attach) != 0 {
		t.Error("a failed paste should stage nothing")
	}
}

type errNoImage struct{}

func (errNoImage) Error() string { return "no image on the clipboard" }

// A session working inside WSL gets a path its own side can open without
// anything being copied. The file is on the Windows disk, and C:\ is not a path
// inside the distribution — but the drive is mounted there, so the same file
// already has a name it can use.
func TestAttachRefCrossesIntoWSL(t *testing.T) {
	host := `C:\Users\someone\AppData\Local\agent-tui\paste-1.png`
	want, ok := vfs.HostToWSL(host)
	if !ok {
		t.Skip("no host-to-WSL mapping on this platform")
	}
	got, err := attachRef(context.Background(), vfs.NewWSL("Ubuntu"), "sess", "paste-1.png", host, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("attachRef gave %q, want %q", got, want)
	}
}

// On the host the file is already where the engine will look, so there is no
// second path to carry and nothing to copy.
func TestAttachRefIsEmptyOnTheHost(t *testing.T) {
	got, err := attachRef(context.Background(), vfs.NewLocal(t.TempDir()),
		"sess", "x.png", "/tmp/x.png", nil)
	if err != nil || got != "" {
		t.Errorf("attachRef gave %q, %v for a local session, want no translation", got, err)
	}
}

// A filesystem with no view of this machine's disk gets the bytes written
// across it, and the path it is told about is the one on its own side.
func TestAttachRefCopiesIntoARemoteFilesystem(t *testing.T) {
	fs := &fakeRemoteFS{}
	data := []byte("\x89PNG\r\n\x1a\nbody")

	got, err := attachRef(context.Background(), fs, "sess-1", "paste-9.png", `C:\tmp\paste-9.png`, data)
	if err != nil {
		t.Fatal(err)
	}
	if want := "/tmp/agent-tui/sess-1/paste-9.png"; got != want {
		t.Errorf("copied to %q, want %q", got, want)
	}
	if fs.wrotePath != got || string(fs.wroteData) != string(data) {
		t.Errorf("wrote %d bytes to %q", len(fs.wroteData), fs.wrotePath)
	}
}

// A container that cannot be written to is reported rather than papered over:
// the built-in engine still sends the image, but a CLI is about to be told
// nothing about it, and that is worth knowing before the turn starts.
func TestAttachRefReportsAFailedCopy(t *testing.T) {
	fs := &fakeRemoteFS{err: errors.New("container is not running")}
	got, err := attachRef(context.Background(), fs, "sess-1", "p.png", `C:\p.png`, []byte("x"))
	if err == nil {
		t.Fatal("a failed copy should be reported")
	}
	if got != "" {
		t.Errorf("a failed copy still produced the path %q", got)
	}
}

// An image staged before the session moved loses its path but not itself.
func TestStaleAttachmentLosesOnlyItsPath(t *testing.T) {
	m := newTestModel(t)
	stagePNG(t, m, 128)
	m.attach[0].Ref, m.attach[0].FS = "/tmp/agent-tui/x.png", "docker:old"

	files := m.takeAttachments("host")

	if len(files) != 1 {
		t.Fatalf("got %d attachments, want the image kept", len(files))
	}
	if files[0].Ref != "" {
		t.Errorf("the stale path %q survived the move", files[0].Ref)
	}
	if files[0].Path == "" {
		t.Error("the image itself was dropped")
	}
}

// fakeRemoteFS is a filesystem that is not this machine's, which is the only
// thing attachRef needs to know about a container.
type fakeRemoteFS struct {
	vfs.FS
	err       error
	wrotePath string
	wroteData []byte
}

func (f *fakeRemoteFS) IsLocal() bool { return false }
func (f *fakeRemoteFS) ID() string    { return "docker:test" }
func (f *fakeRemoteFS) Label() string { return "test" }

func (f *fakeRemoteFS) WriteFile(_ context.Context, path string, data []byte) error {
	if f.err != nil {
		return f.err
	}
	f.wrotePath, f.wroteData = path, data
	return nil
}
