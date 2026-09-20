package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/phanngoc/agent-tui/internal/clipboard"
	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// Pasting an image into the prompt.
//
// The terminal is no help here: an image on the clipboard is not part of a
// paste, so nothing arrives and the key appears to do nothing. The image has to
// be fetched from the operating system, written somewhere both this process and
// the session's own filesystem can reach, and carried on the message until it
// is sent.

// pastedMsg is the result of reading the clipboard, which is a round trip to a
// helper process and so cannot be done in the key handler.
type pastedMsg struct {
	att session.Attachment
	err error
	// warn is a staged image the agent's own side could not be given a copy
	// of. The built-in engine still sends the bytes, so this is a smaller
	// problem than a failure and is reported as one.
	warn string
}

// pasteImage stages the clipboard's image for the next prompt.
func (m *Model) pasteImage() tea.Cmd {
	s := m.mgr.Active()
	dir := attachDir(s.ID)
	name := fmt.Sprintf("paste-%d.png", time.Now().UnixMilli())
	fsys := m.sessionFS(s)

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		data, err := clipboard.Image(ctx)
		if err != nil {
			return pastedMsg{err: err}
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return pastedMsg{err: err}
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return pastedMsg{err: err}
		}

		att := session.Attachment{
			Path:  path,
			Media: "image/png",
			Bytes: int64(len(data)),
			FS:    fsID(fsys),
		}
		ref, err := attachRef(ctx, fsys, s.ID, name, path, data)
		att.Ref = ref
		out := pastedMsg{att: att}
		if err != nil {
			out.warn = "attached, but " + fsys.Label() + " could not be given a copy: " +
				err.Error()
		}
		return out
	}
}

// onPasted records a staged image and marks its place in the prompt.
func (m *Model) onPasted(msg pastedMsg) {
	if msg.err != nil {
		m.notice = ""
		m.errText = msg.err.Error()
		return
	}
	m.attach = append(m.attach, msg.att)
	// A marker in the text is what makes the attachment referable: "the error
	// in image #2" only means something if the numbering is visible.
	marker := "[image #" + strconv.Itoa(len(m.attach)) + "]"
	if v := m.input.Value(); v != "" && !strings.HasSuffix(v, " ") && !strings.HasSuffix(v, "\n") {
		marker = " " + marker
	}
	m.input.InsertString(marker + " ")
	m.errText = ""
	m.notice = "attached " + byteSize(msg.att.Bytes) + " image"
	if msg.warn != "" {
		m.notice = ""
		m.errText = msg.warn
	}
	// The prompt box grew a row, and the panes above it have to give it up.
	m.resize(m.w, m.h)
}

// dropAttachments clears what was staged. Clearing the prompt clears them with
// it: the markers that referred to them are gone, so keeping the files staged
// would mean sending pictures nothing in the text points at.
func (m *Model) dropAttachments() {
	if len(m.attach) == 0 {
		return
	}
	m.attach = nil
	m.resize(m.w, m.h)
}

// takeAttachments hands the staged images to the message being sent.
//
// A session can be repointed between pasting an image and sending it, and the
// copy made for the container it used to be aimed at then names a path nothing
// on this side can open. That reference is dropped rather than corrected: the
// image still goes out whole to an engine that can carry one, and an engine
// that cannot is better told nothing than told where to find a file that is
// not there.
func (m *Model) takeAttachments(fs string) []session.Attachment {
	if len(m.attach) == 0 {
		return nil
	}
	out := m.attach
	stale := 0
	for i := range out {
		if out[i].FS != fs {
			out[i].Ref, out[i].FS = "", fs
			stale++
		}
	}
	if stale > 0 {
		m.notice = plural(stale, "image") + " was attached before this session moved"
	}
	m.attach = nil
	m.resize(m.w, m.h)
	return out
}

// attachRows is how many rows the prompt box needs for the staged images, so
// the layout can hand them over rather than let them overlap the panes.
func (m *Model) attachRows() int {
	if len(m.attach) == 0 {
		return 0
	}
	return 1
}

// attachBar lists what will go out with the next prompt. An attachment is
// otherwise invisible — the marker in the text is only a marker — and a thing
// you cannot see is a thing you will send by accident.
func (m *Model) attachBar() string {
	parts := make([]string, 0, len(m.attach)+1)
	for i, a := range m.attach {
		parts = append(parts, m.st.MdMark.Render("[image #"+strconv.Itoa(i+1)+"]")+" "+
			m.st.Dim.Render(byteSize(a.Bytes)))
	}
	parts = append(parts, m.st.Faint.Render("ctrl+u drops them"))
	return clipLine(" "+strings.Join(parts, "   "), max(3, m.w-2))
}

// attachDir is where staged images live: outside the project, because they are
// not part of it, and keyed by session so closing one does not orphan another's
// files in a directory nobody ever looks at.
func attachDir(sessionID string) string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "agent-tui", "attachments", sessionID)
}

// attachRef puts the staged image within reach of wherever this session's
// agent runs, and reports the path it would open it by.
//
// The three cases are three different amounts of work. On the host the file is
// already where it needs to be. A WSL distribution has this machine's drives
// mounted, so the same file already has a name in there and translating it
// costs nothing. A container has no view of this disk at all, so the bytes are
// written across — through the session's own filesystem, which is a `docker
// exec ... cat >` and therefore works for anything else that ever implements
// the interface.
func attachRef(ctx context.Context, fsys vfs.FS, sessionID, name, host string,
	data []byte) (string, error) {

	if fsys == nil || fsys.IsLocal() {
		return "", nil
	}
	if _, ok := fsys.(*vfs.WSL); ok {
		if p, ok := vfs.HostToWSL(host); ok {
			return p, nil
		}
		// A file outside the mounted drives — a RAM disk, a network share —
		// has no name in there, so it is copied like a container's would be.
	}
	dest := vfs.Join(remoteAttachDir, sessionID, name)
	if err := fsys.WriteFile(ctx, dest, data); err != nil {
		return "", err
	}
	return dest, nil
}

// remoteAttachDir is where a copy lands on the far side. /tmp because it is the
// one writable directory every image and distribution agrees on, and because a
// pasted screenshot is not something anyone wants to find again next week.
const remoteAttachDir = "/tmp/agent-tui"

func fsID(fsys vfs.FS) string {
	if fsys == nil {
		return ""
	}
	return fsys.ID()
}

func byteSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return strconv.FormatInt(n>>10, 10) + "KB"
	default:
		return strconv.FormatInt(n, 10) + "B"
	}
}
