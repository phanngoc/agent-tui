package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/config"
	"github.com/phanngoc/agent-tui/internal/gateway"
	"github.com/phanngoc/agent-tui/internal/session"
)

// The board is the user's view of conversations as tasks: a move is saved
// on the conversation and leaves when it was last talked in alone, and the
// links it was about are found and can be pinned.
func TestBoardAndRefs(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	m := session.NewManager(config.DataDir(), root, "")
	s := m.New()
	s.Append(session.Message{Role: session.RoleUser, At: time.Now(), Text: "phân tích lỗi https://sun-vn.slack.com/archives/C0A8WMPPTEF/p1759991234567890"})
	s.Append(session.Message{Role: session.RoleAssistant, At: time.Now(), Text: "Nguyên nhân ở https://github.com/o/r/pull/5"})
	m.SaveNow(s)
	m.Shutdown()
	before, _ := gateway.Load(s.ID)

	srv := New(config.Default(), "test", "")
	ts := httptest.NewServer(srv.guard(srv.mux))
	defer ts.Close()
	put := func(body string) int {
		req, _ := http.NewRequest("PUT", ts.URL+"/api/sessions/"+s.ID+"/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if code := put(`{"board":"doing","board_rank":1.5,"refs":[{"url":"https://nulab.backlog.com/view/FPAAS-42","title":"  the   ticket "},{"url":"https://nulab.backlog.com/view/FPAAS-42"}]}`); code != 200 {
		t.Fatalf("move: %d", code)
	}
	got, _ := gateway.Load(s.ID)
	if got.Board != "doing" || got.BoardRank != 1.5 {
		t.Fatalf("board not saved: %q %v", got.Board, got.BoardRank)
	}
	if len(got.Refs) != 1 || got.Refs[0].Title != "the ticket" || got.Refs[0].Added.IsZero() {
		t.Fatalf("refs: %+v", got.Refs)
	}
	if !got.Updated.Equal(before.Updated) {
		t.Fatal("a move on the board changed when the conversation was last talked in")
	}
	if code := put(`{"board":"running"}`); code != 400 {
		t.Fatalf("a column that is not one: %d", code)
	}
	if code := put(`{"refs":[{"url":"javascript:alert(1)"}]}`); code != 400 {
		t.Fatalf("not a web link: %d", code)
	}
	if code := put(`{"board":"backlog"}`); code != 200 {
		t.Fatal("back to the backlog")
	}
	if got, _ := gateway.Load(s.ID); got.Board != "" {
		t.Fatalf("the backlog is stored as empty: %q", got.Board)
	}

	resp, err := http.Get(ts.URL + "/api/sessions/" + s.ID + "/refs")
	if err != nil {
		t.Fatal(err)
	}
	var refs struct {
		Pinned []struct{ URL, Kind, Label string } `json:"pinned"`
		Found  []session.Link                      `json:"found"`
		Origin *session.Link                       `json:"origin"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&refs)
	resp.Body.Close()
	if len(refs.Pinned) != 1 || refs.Pinned[0].Kind != "backlog" || len(refs.Found) != 2 ||
		refs.Found[0].Kind != "slack" || refs.Found[1].Role != "assistant" || refs.Origin == nil || refs.Origin.Kind != "backlog" {
		t.Fatalf("refs: %+v", refs)
	}

	resp, err = http.Get(ts.URL + "/api/sessions?root=" + root)
	if err != nil {
		t.Fatal(err)
	}
	var list []gateway.Summary
	_ = json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list) != 1 || list[0].Refs != 3 || list[0].Origin == nil || list[0].Origin.Label != "the ticket" {
		t.Fatalf("summary: %+v", list)
	}
}
