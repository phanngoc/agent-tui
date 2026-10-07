package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/phanngoc/agent-tui/internal/gateway"
	"github.com/phanngoc/agent-tui/internal/session"
)

// Images for a prompt, from the web: dropped, pasted or picked in the
// composer, uploaded here, sent with the prompt by path
// (gateway.CheckAttachments keeps it to these files).

const maxAttachment = 20 << 20

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func (s *Server) attachRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/attachments", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxAttachment+1<<20)
		if err := r.ParseMultipartForm(maxAttachment); err != nil {
			fail(w, http.StatusBadRequest, errors.New("an image of at most 20 MB, as multipart form field \"file\""))
			return
		}
		f, h, err := r.FormFile("file")
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		defer f.Close()
		head := make([]byte, 512)
		n, _ := io.ReadFull(f, head)
		media := http.DetectContentType(head[:n])
		if !strings.HasPrefix(media, "image/") {
			fail(w, http.StatusBadRequest, errors.New("only images can be attached (got "+media+")"))
			return
		}
		dir := filepath.Join(gateway.AttachDir(), time.Now().Format("20060102"))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		b := make([]byte, 4)
		_, _ = rand.Read(b)
		name := unsafeName.ReplaceAllString(filepath.Base(h.Filename), "-")
		if name == "" || name == "." || name == "-" {
			name = "image"
		}
		if filepath.Ext(name) == "" {
			name += map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}[media]
		}
		path := filepath.Join(dir, hex.EncodeToString(b)+"-"+name)
		out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		size, err := io.Copy(out, io.MultiReader(strings.NewReader(string(head[:n])), f))
		out.Close()
		if err != nil {
			os.Remove(path)
			fail(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, session.Attachment{Path: path, Media: media, Bytes: size})
	})
	// The image itself, for a thumbnail: only an uploaded one.
	m.HandleFunc("GET /api/attachments", func(w http.ResponseWriter, r *http.Request) {
		files, err := gateway.CheckAttachments([]session.Attachment{{Path: r.URL.Query().Get("path")}})
		if err != nil || len(files) != 1 {
			fail(w, http.StatusNotFound, gateway.ErrNotAnAttachment)
			return
		}
		w.Header().Set("Cache-Control", "private, max-age=86400")
		http.ServeFile(w, r, files[0].Path)
	})
}
