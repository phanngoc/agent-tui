package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/phanngoc/agent-tui/internal/awake"
	"github.com/phanngoc/agent-tui/internal/config"
)

// The keep-awake setting from the admin holds the machine awake, or not, at
// once: always holds with nothing running, busy holds nothing while idle,
// off lets go.
func TestKeepAwakeFollowsTheSetting(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	srv := New(config.Default(), "test", "")
	defer srv.awake.Close()
	h := srv.guard(srv.mux)
	put := func(body string) {
		r := httptest.NewRequest("PUT", "http://127.0.0.1/api/settings/global", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("settings: %d %s", w.Code, w.Body)
		}
	}
	status := func() awake.Status {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1/api/awake", nil))
		var st awake.Status
		_ = json.Unmarshal(w.Body.Bytes(), &st)
		return st
	}
	if st := status(); st.Mode != awake.ModeBusy || st.Active {
		t.Fatalf("default: %+v", st)
	}
	if !status().Supported {
		t.Skip("no way to keep this system awake here")
	}
	put(`{"keep_awake":"always","keep_display":true}`)
	if st := status(); !st.Active || !st.Display || st.Reason == "" {
		t.Fatalf("always: %+v", st)
	}
	put(`{"keep_awake":"off"}`)
	if st := status(); st.Active {
		t.Fatalf("off: %+v", st)
	}
}
