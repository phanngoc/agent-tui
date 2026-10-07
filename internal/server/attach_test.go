package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phanngoc/agent-tui/internal/config"
	"github.com/phanngoc/agent-tui/internal/session"
)

// A picture dropped in the composer is uploaded, can be shown back, and
// goes with a prompt by path; nothing else of this machine can.
func TestAttachmentsUploadServeAndGuard(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("LocalAppData", t.TempDir()) // os.UserCacheDir on Windows
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv := New(config.Default(), "test", "")
	h := srv.guard(srv.mux)
	upload := func(name string, data []byte) *httptest.ResponseRecorder {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, _ := mw.CreateFormFile("file", name)
		_, _ = fw.Write(data)
		_ = mw.Close()
		r := httptest.NewRequest("POST", "http://127.0.0.1/api/attachments", &body)
		r.Header.Set("Content-Type", mw.FormDataContentType())
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 64)...)
	w := upload("screen shot.png", png)
	var att session.Attachment
	_ = json.Unmarshal(w.Body.Bytes(), &att)
	if w.Code != 200 || att.Media != "image/png" || att.Bytes != int64(len(png)) || !strings.HasSuffix(att.Path, "-screen-shot.png") {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	if w := upload("notes.txt", []byte("hello")); w.Code != http.StatusBadRequest {
		t.Fatalf("a text file was taken: %d", w.Code)
	}
	get := func(p string) int {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1/api/attachments?path="+p, nil))
		return w.Code
	}
	if get(att.Path) != 200 {
		t.Fatal("the uploaded image is not served back")
	}
	secret := filepath.Join(t.TempDir(), "secret.png")
	_ = os.WriteFile(secret, png, 0o644)
	if get(secret) != http.StatusNotFound {
		t.Fatal("a file outside the uploads was served")
	}
	body, _ := json.Marshal(map[string]any{"root": t.TempDir(), "prompt": "look", "files": []map[string]string{{"path": secret}}})
	r := httptest.NewRequest("POST", "http://127.0.0.1/api/sessions", bytes.NewReader(body))
	pw := httptest.NewRecorder()
	h.ServeHTTP(pw, r)
	if pw.Code != http.StatusBadRequest {
		t.Fatalf("a prompt carried a file outside the uploads: %d %s", pw.Code, pw.Body)
	}
}
