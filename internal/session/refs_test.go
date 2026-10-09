package session

import (
	"strings"
	"testing"
	"time"
)

func TestLinksFoundInWhatWasSaid(t *testing.T) {
	now := time.Now()
	s := &Session{Messages: []Message{
		{Role: RoleUser, At: now, Text: "phân tích lỗi này: https://sun-vn.slack.com/archives/C0A8WMPPTEF/p1759991234567890, xem thêm (https://github.com/framgia/sbi-fpaas-be/pull/1761)."},
		{Role: RoleAssistant, At: now, Text: "Đã đọc https://github.com/framgia/sbi-fpaas-be/pull/1761 và spec https://docs.google.com/spreadsheets/d/abc/edit#gid=0"},
		{Role: RoleUser, At: now, Text: "lại link cũ https://sun-vn.slack.com/archives/C0A8WMPPTEF/p1759991234567890"},
	}}
	s.Messages[1].Tools = []ToolCall{{Name: "fetch", Result: "https://ignored.example.com/in/tool/output"}}
	links := s.Links()
	if len(links) != 3 {
		t.Fatalf("got %d links: %+v", len(links), links)
	}
	slack, gh, sheet := links[0], links[1], links[2]
	wantLabel := "Slack thread · " + time.Unix(1759991234, 0).Local().Format("2006-01-02 15:04")
	if slack.Kind != "slack" || slack.Label != wantLabel || slack.At != 0 || slack.Role != "user" || slack.Times != 2 {
		t.Errorf("slack: %+v", slack)
	}
	if gh.Kind != "github" || gh.Label != "framgia/sbi-fpaas-be#1761" || !strings.HasSuffix(gh.URL, "/1761") || gh.Times != 2 {
		t.Errorf("github (trailing ')' and '.' must be trimmed): %+v", gh)
	}
	if sheet.Kind != "google" || sheet.Label != "Google Sheet" || sheet.Role != "assistant" {
		t.Errorf("sheet: %+v", sheet)
	}
	if !strings.Contains(slack.Snippet, "phân tích lỗi") {
		t.Errorf("snippet: %q", slack.Snippet)
	}

	o, ok := s.Origin()
	if !ok || o.URL != slack.URL {
		t.Fatalf("origin from the user's first link: %+v", o)
	}
	s.Refs = []Ref{{URL: "https://nulab.backlog.com/view/FPAAS-42", Title: "the ticket"}}
	if o, _ := s.Origin(); o.URL != s.Refs[0].URL || o.Label != "the ticket" || o.Kind != "backlog" {
		t.Fatalf("a pinned link is the origin: %+v", o)
	}
}

func TestClassify(t *testing.T) {
	for in, want := range map[string][2]string{
		"https://github.com/a/b/issues/7":           {"github", "a/b#7"},
		"https://github.com/a/b":                    {"github", "a/b"},
		"https://x.atlassian.net/browse/ABC-1":      {"jira", "ABC-1"},
		"https://x.backlog.jp/view/P-9":             {"backlog", "P-9"},
		"https://miro.com/app/board/uXjV=/":         {"miro", "Miro board"},
		"https://example.com/docs/guide":            {"web", "example.com/…/guide"},
		"https://example.com/readme":                {"web", "example.com/readme"},
		"https://sun-vn.slack.com/archives/C1":      {"slack", "Slack C1"},
		"https://docs.google.com/document/d/1/edit": {"google", "Google Doc"},
	} {
		if k, l := Classify(in); k != want[0] || l != want[1] {
			t.Errorf("Classify(%s) = %s, %q; want %s, %q", in, k, l, want[0], want[1])
		}
	}
	if !ValidBoard("") || !ValidBoard("doing") || ValidBoard("running") {
		t.Error("ValidBoard")
	}
}
