package gateway

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// Images sent with a prompt from the web: uploaded to a folder of the
// gateway's own, carried on the message by path, and put within reach of
// wherever the session's agent runs — the same three cases as a picture
// pasted in a terminal (internal/ui/attach.go): on this machine the file is
// where it needs to be, a WSL distribution sees it through /mnt, a container
// gets a copy.

// AttachDir is where uploaded images wait to be sent.
func AttachDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "agent-tui", "attachments", "web")
}

// remoteAttachDir is where a copy lands on the far side of a container.
const remoteAttachDir = "/tmp/agent-tui"

// ErrNotAnAttachment is a path that is not an uploaded image: a prompt
// cannot have the agent read just any file of this machine, least of all
// one sent through the tunnel.
var ErrNotAnAttachment = errors.New("not an uploaded attachment")

// CheckAttachments keeps only uploaded images, refusing anything else.
func CheckAttachments(files []session.Attachment) ([]session.Attachment, error) {
	if len(files) == 0 {
		return nil, nil
	}
	dir, err := filepath.Abs(AttachDir())
	if err != nil {
		return nil, err
	}
	out := make([]session.Attachment, 0, len(files))
	for _, f := range files {
		p, err := filepath.Abs(f.Path)
		if err != nil {
			return nil, ErrNotAnAttachment
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			return nil, ErrNotAnAttachment
		}
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			return nil, ErrNotAnAttachment
		}
		out = append(out, session.Attachment{Path: p, Media: f.Media, Bytes: st.Size()})
	}
	return out, nil
}

// reach gives each image the path the session's own filesystem opens it by.
func reach(fsys vfs.FS, sessionID string, files []session.Attachment) []session.Attachment {
	if len(files) == 0 || fsys == nil || fsys.IsLocal() {
		return files
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out := make([]session.Attachment, len(files))
	for i, f := range files {
		f.FS = fsys.ID()
		if _, wsl := fsys.(*vfs.WSL); wsl {
			if p, ok := vfs.HostToWSL(f.Path); ok {
				f.Ref = p
				out[i] = f
				continue
			}
		}
		if data, err := os.ReadFile(f.Path); err == nil {
			dest := vfs.Join(remoteAttachDir, sessionID, filepath.Base(f.Path))
			if fsys.WriteFile(ctx, dest, data) == nil {
				f.Ref = dest
			}
		}
		out[i] = f
	}
	return out
}
