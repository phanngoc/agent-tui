package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

func stageImage(t *testing.T, body string) session.Attachment {
	t.Helper()
	path := filepath.Join(t.TempDir(), "paste.png")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return session.Attachment{Path: path, Media: "image/png", Bytes: int64(len(body))}
}

// The image goes ahead of the question, which is both what the API asks for and
// the order a reader would use: you look at the screenshot, then at what was
// asked about it.
func TestUserBlocksPutsTheImageFirst(t *testing.T) {
	att := stageImage(t, "\x89PNG\r\n\x1a\nbody")
	blocks := UserBlocks("cái này là gì?", []session.Attachment{att})

	if len(blocks) != 2 {
		t.Fatalf("got %d blocks, want an image and a question", len(blocks))
	}
	raw, err := json.Marshal(blocks[0])
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Type   string `json:"type"`
		Source struct {
			Type      string `json:"type"`
			MediaType string `json:"media_type"`
			Data      string `json:"data"`
		} `json:"source"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != "image" || got.Source.MediaType != "image/png" {
		t.Errorf("first block is %s/%s, want an image/png", got.Type, got.Source.MediaType)
	}
	if got.Source.Data == "" {
		t.Error("the image went out with no data")
	}
}

// The bytes live outside the session, so a swept-up temporary file must not
// take the rest of the conversation with it.
func TestUserBlocksSkipsAMissingFile(t *testing.T) {
	gone := session.Attachment{Path: filepath.Join(t.TempDir(), "not-here.png")}
	blocks := UserBlocks("vẫn hỏi được", []session.Attachment{gone})

	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want just the text", len(blocks))
	}
}

// A message with an image and nothing typed is still a message.
func TestUserBlocksAllowsAnImageAlone(t *testing.T) {
	att := stageImage(t, "\x89PNG\r\n\x1a\nbody")
	if got := len(UserBlocks("   ", []session.Attachment{att})); got != 1 {
		t.Errorf("got %d blocks, want the image on its own", got)
	}
	if got := len(UserBlocks("", nil)); got != 0 {
		t.Errorf("an empty message produced %d blocks", got)
	}
}

// Replay rebuilds the images too, or resuming a session would quietly drop the
// screenshot the whole conversation is about.
func TestReplayRestoresAttachments(t *testing.T) {
	att := stageImage(t, "\x89PNG\r\n\x1a\nbody")
	msgs := []session.Message{{
		Role: session.RoleUser, Text: "cái này là gì?", Files: []session.Attachment{att},
	}}
	out := Replay(msgs)
	if len(out) != 1 {
		t.Fatalf("got %d messages", len(out))
	}
	if len(out[0].Content) != 2 {
		t.Errorf("the replayed turn has %d blocks, want the image and the text",
			len(out[0].Content))
	}
}

// An engine that can only be handed text is handed the path instead. Every CLI
// here can open a file, and a path it can open beats an attachment it cannot
// receive.
func TestPromptTextNamesTheFileForALocalCLI(t *testing.T) {
	turn := Turn{
		Prompt: "xem ảnh này",
		Files:  []session.Attachment{{Path: "/tmp/b.png"}},
	}
	got := turn.PromptText()
	if !strings.HasPrefix(got, "xem ảnh này") || !strings.Contains(got, "/tmp/b.png") {
		t.Errorf("prompt is %q", got)
	}
	if plain := (Turn{Prompt: "chỉ chữ"}).PromptText(); plain != "chỉ chữ" {
		t.Errorf("a turn with no files became %q", plain)
	}
}

// A CLI running somewhere else is told where the file is from there. The path
// on this machine would send it looking on the wrong side of the boundary.
func TestPromptTextNamesTheFarSidePath(t *testing.T) {
	turn := Turn{
		Prompt: "xem ảnh này",
		FS:     remoteFS{},
		Files: []session.Attachment{
			{Path: `C:\tmp\a.png`, Ref: "/mnt/c/tmp/a.png"},
			// Never got across: naming a path it cannot open is worse than
			// saying nothing, so this one is left out.
			{Path: `C:\tmp\b.png`},
		},
	}
	got := turn.PromptText()
	if !strings.Contains(got, "/mnt/c/tmp/a.png") {
		t.Errorf("prompt lost the image's far-side path:\n%s", got)
	}
	if strings.Contains(got, `C:\tmp\`) {
		t.Errorf("prompt names a path from this machine:\n%s", got)
	}
	if strings.Count(got, "Attached image:") != 1 {
		t.Errorf("prompt names an image that never got across:\n%s", got)
	}
}

// remoteFS stands in for a container or a distribution: a filesystem that is
// not this machine's, which is all PromptText needs to know.
type remoteFS struct{ vfs.FS }

func (remoteFS) IsLocal() bool { return false }
