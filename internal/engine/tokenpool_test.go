package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/session"
)

func TestPoolUsageTalliesEachToken(t *testing.T) {
	dir := t.TempDir()
	if r := poolUsage(dir, time.Now()); r.Enabled || len(r.Tokens) != 0 {
		t.Fatalf("no pool reported as %+v", r)
	}
	cred := `<Obj><Props><S N="UserName">claude</S><SS N="Password">x</SS></Props></Obj>`
	if err := os.WriteFile(filepath.Join(dir, "pool.xml"), []byte("<Objs>"+cred+cred+cred+"</Objs>"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := "\uFEFF" + `{"version":1,"next":2,"assignments":{"k1":"aaaaaaaaaaaa0000","k2":"aaaaaaaaaaaa0000","k3":"bbbbbbbbbbbb1111"}}`
	if err := os.WriteFile(filepath.Join(dir, "agent-tui-state.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 10, 7, 15, 0, 0, 0, time.Local)
	a := session.Credential{Slot: 1, Of: 3, ID: "aaaaaaaaaaaa"}
	b := session.Credential{Slot: 2, Of: 3, ID: "bbbbbbbbbbbb"}
	limits := []agent.Limit{{Window: "five_hour", Used: 0.4, Resets: now.Add(time.Hour).Truncate(time.Second)}}
	recordUsage(dir, a, agent.EvUsage{In: 10, Out: 5, CacheRead: 100, CacheWrite: 7}, now.AddDate(0, 0, -10))
	recordUsage(dir, a, agent.EvUsage{In: 1, Out: 2, CacheRead: 3, CacheWrite: 4, Limits: limits}, now.Add(-time.Hour))
	recordUsage(dir, b, agent.EvUsage{In: 20, Out: 30}, now.AddDate(0, 0, -2))
	recordUsage(dir, session.Credential{}, agent.EvUsage{In: 999}, now) // drawn from no pool: not recorded

	r := poolUsage(dir, now)
	if !r.Enabled || r.Size != 3 || len(r.Tokens) != 3 {
		t.Fatalf("report: %+v", r)
	}
	ta, tb, tc := r.Tokens[0], r.Tokens[1], r.Tokens[2]
	if ta.ID != a.ID || ta.Total != (Tally{Turns: 2, In: 11, Out: 7, CacheRead: 103, CacheWrite: 11}) || ta.Today.Turns != 1 || ta.Week.Turns != 1 {
		t.Fatalf("token a: %+v", ta)
	}
	if ta.Conversations != 2 || len(ta.Limits) != 1 || ta.Limits[0].Used != 0.4 || !ta.Limits[0].Resets.Equal(limits[0].Resets) {
		t.Fatalf("token a conversations or limits: %+v", ta)
	}
	if tb.ID != b.ID || tb.Today.Turns != 0 || tb.Week.In != 20 || tb.Conversations != 1 {
		t.Fatalf("token b: %+v", tb)
	}
	if tc.Slot != 3 || tc.ID != "" || tc.Total.Turns != 0 {
		t.Fatalf("unused slot: %+v", tc)
	}
}

func TestClaudeDecoderCarriesRateLimitsToUsage(t *testing.T) {
	var got []agent.EvUsage
	d := &claudeDec{}
	emit := func(e agent.Event) {
		if u, ok := e.(agent.EvUsage); ok {
			got = append(got, u)
		}
	}
	d.line([]byte(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","rateLimitType":"five_hour","resetsAt":1791373800,"unifiedWindows":{"seven_day":{"utilization":0.51,"resetsAt":1791691200},"five_hour":{"utilization":0.36,"resetsAt":1791373800}}}}`), emit)
	d.line([]byte(`{"type":"result","subtype":"success","usage":{"input_tokens":4,"output_tokens":676,"cache_read_input_tokens":35104,"cache_creation_input_tokens":27476}}`), emit)
	if len(got) != 1 {
		t.Fatalf("usage events: %d", len(got))
	}
	u := got[0]
	if u.In != 4 || u.Out != 676 || u.CacheRead != 35104 || u.CacheWrite != 27476 {
		t.Fatalf("usage: %+v", u)
	}
	if len(u.Limits) != 2 || u.Limits[0].Window != "five_hour" || u.Limits[0].Used != 0.36 || u.Limits[1].Window != "seven_day" || u.Limits[1].Resets.Unix() != 1791691200 {
		t.Fatalf("limits: %+v", u.Limits)
	}
}
