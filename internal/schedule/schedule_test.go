package schedule

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestCronNext(t *testing.T) {
	cases := []struct{ expr, from, want string }{
		{"*/15 * * * *", "2026-10-05 10:07", "2026-10-05 10:15"},
		{"0 9 * * 1-5", "2026-10-09 09:00", "2026-10-12 09:00"}, // Fri 9:00 → Mon
		{"30 14 15 3 *", "2026-10-05 10:00", "2027-03-15 14:30"},
		{"0 0 1 * 0", "2026-10-05 10:00", "2026-10-11 00:00"}, // the 1st OR a Sunday
		{"5/20 8 * * *", "2026-10-05 08:06", "2026-10-05 08:25"},
		{"0 12 * * 7", "2026-10-05 10:00", "2026-10-11 12:00"}, // 7 is Sunday
	}
	for _, c := range cases {
		spec, err := parseCron(c.expr)
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		got, ok := spec.next(mustTime(t, c.from))
		if !ok || !got.Equal(mustTime(t, c.want)) {
			t.Errorf("%s after %s = %s; want %s", c.expr, c.from, got.Format("2006-01-02 15:04"), c.want)
		}
	}
	for _, bad := range []string{"* * *", "61 * * * *", "* * * * MON", "*/0 * * * *"} {
		if _, err := parseCron(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	if _, ok := mustCron(t, "0 0 31 2 *").next(mustTime(t, "2026-01-01 00:00")); ok {
		t.Error("Feb 31 matched")
	}
}

func mustCron(t *testing.T, e string) cronSpec {
	c, err := parseCron(e)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestEveryIsAnchoredAndWindowsFit(t *testing.T) {
	created := mustTime(t, "2026-10-05 08:10")
	j := &Job{ID: "a", Every: "6h", Created: created}
	got, _ := j.NextAfter(mustTime(t, "2026-10-05 15:00"))
	if !got.Equal(mustTime(t, "2026-10-05 20:10")) {
		t.Fatalf("every 6h from 08:10, after 15:00 = %s", got)
	}
	j.ActiveHours = &Window{Start: "09:00", End: "18:00", Days: []int{1, 2, 3, 4, 5}}
	got, _ = j.NextAfter(mustTime(t, "2026-10-09 15:00")) // Fri: 20:10 is closed → Mon 09:00
	if !got.Equal(mustTime(t, "2026-10-12 09:00")) {
		t.Fatalf("windowed = %s", got)
	}
	night := &Window{Start: "22:00", End: "06:00"}
	if !night.inWindow(mustTime(t, "2026-10-05 23:30")) || !night.inWindow(mustTime(t, "2026-10-06 05:59")) || night.inWindow(mustTime(t, "2026-10-06 12:00")) {
		t.Fatal("a window across midnight")
	}
}

func TestSilentAckAndEmptyChecklist(t *testing.T) {
	for _, s := range []string{"HEARTBEAT_OK", "**HEARTBEAT_OK**", "All quiet. NO_REPLY", "NO_REPLY\n"} {
		if !SilentAck(s) {
			t.Errorf("%q is an ack", s)
		}
	}
	for _, s := range []string{"CI failed on main: HEARTBEAT_OK is wrong here, see the log", "", strings.Repeat("x", 400) + " HEARTBEAT_OK"} {
		if SilentAck(s) {
			t.Errorf("%q is not an ack", s)
		}
	}
	if !checklistEmpty("# Heartbeat\n\n<!-- add items\nhere -->\n- [ ]\n```\n```\n") {
		t.Error("a checklist of headings and stubs is empty")
	}
	if checklistEmpty("# Heartbeat\n- [ ] check CI on main\n") {
		t.Error("a checklist with an item is not empty")
	}
}

// fakeHost runs turns instantly, answering what it is told.
type fakeHost struct {
	mu      sync.Mutex
	prompts []string
	answer  string
	errText string
	gateOK  bool
	busy    map[string]bool
	n       int
}

func (h *fakeHost) Start(j *Job, prompt string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.prompts = append(h.prompts, prompt)
	h.n++
	if j.Session == SessionSame && j.SessionID != "" {
		return j.SessionID, nil
	}
	return "s" + string(rune('0'+h.n)), nil
}
func (h *fakeHost) Busy(s string) bool { h.mu.Lock(); defer h.mu.Unlock(); return h.busy[s] }
func (h *fakeHost) Cancel(string)      {}
func (h *fakeHost) Result(string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.answer
}

// done ends a run's turn the way the gateway reports it.
func (hs *harness) done(sess string) { hs.s.TurnDone(sess, hs.h.errText) }
func (h *fakeHost) Gate(context.Context, *Job) (string, bool, error) {
	return "2 checks failing", h.gateOK, nil
}

type harness struct {
	t   *testing.T
	s   *Scheduler
	h   *fakeHost
	now time.Time
}

func newHarness(t *testing.T) *harness {
	hs := &harness{t: t, h: &fakeHost{busy: map[string]bool{}, gateOK: true}, now: mustTime(t, "2026-10-05 10:00")}
	hs.s = &Scheduler{Store: &Store{Dir: t.TempDir()}, Host: hs.h, now: func() time.Time { return hs.now }}
	hs.s.mu.Lock()
	hs.s.init()
	hs.s.started = hs.now
	hs.s.mu.Unlock()
	return hs
}

func (hs *harness) add(j *Job) *Job {
	j.Enabled = true
	if j.Root == "" {
		j.Root = hs.t.TempDir()
	}
	if err := j.Validate(); err != nil {
		hs.t.Fatal(err)
	}
	if err := hs.s.Store.Put(j); err != nil {
		hs.t.Fatal(err)
	}
	hs.s.Saved(j.ID)
	got, _ := hs.s.Store.Get(j.ID)
	return got
}

// fire advances the clock to the job's next run, ticks, and waits for the
// run to start (or be skipped); it returns the session, if one started.
func (hs *harness) fire(id string) string {
	hs.t.Helper()
	j, _ := hs.s.Store.Get(id)
	if j.State.Next.After(hs.now) {
		hs.now = j.State.Next
	}
	before := len(hs.s.Store.Runs(id, 0))
	for i := 0; i < 400; i++ {
		// Ticked again now and then: the run before may have been recorded
		// and still be finishing (its job not yet out of pending), and a
		// tick then passes the job by.
		if i%20 == 0 {
			// The run before may also have moved the job's next time
			// after it was read.
			if j, _ := hs.s.Store.Get(id); j.State.Next.After(hs.now) {
				hs.now = j.State.Next
			}
			hs.s.Tick()
		}
		j, _ = hs.s.Store.Get(id)
		if j.State.Running != "" {
			return j.State.Running
		}
		if len(hs.s.Store.Runs(id, 0)) > before {
			return ""
		}
		time.Sleep(5 * time.Millisecond)
	}
	hs.t.Fatal("the job did not fire")
	return ""
}

func (hs *harness) job(id string) *Job { j, _ := hs.s.Store.Get(id); return j }

func TestATaskRunsAndIsRescheduled(t *testing.T) {
	hs := newHarness(t)
	j := hs.add(&Job{Name: "review", Kind: KindTask, Prompt: "review open PRs", Every: "1h"})
	if want := hs.now.Add(time.Hour); !j.State.Next.Equal(want) {
		t.Fatalf("next = %s; want %s", j.State.Next, want)
	}
	hs.h.answer = "PR 12 needs a rebase."
	sess := hs.fire(j.ID)
	if !strings.Contains(hs.h.prompts[0], "[scheduled: review · scheduled") || !strings.Contains(hs.h.prompts[0], "NO_REPLY") {
		t.Fatalf("prompt = %q", hs.h.prompts[0])
	}
	hs.done(sess)
	j = hs.job(j.ID)
	if j.State.LastStatus != StatusOK || j.State.LastText != "PR 12 needs a rebase." || j.State.Running != "" {
		t.Fatalf("state = %+v", j.State)
	}
	if want := hs.now.Add(time.Hour); !j.State.Next.Equal(want) {
		t.Fatalf("next = %s; want %s", j.State.Next, want)
	}
	hs.h.answer = "NO_REPLY"
	hs.done(hs.fire(j.ID))
	if hs.job(j.ID).State.LastStatus != StatusSilent {
		t.Fatal("NO_REPLY was not silent")
	}
}

func TestFailuresBackOffThenPause(t *testing.T) {
	hs := newHarness(t)
	j := hs.add(&Job{Name: "flaky", Kind: KindTask, Prompt: "x", Every: "1h"})
	hs.h.errText = "overloaded"
	want := []time.Duration{30 * time.Second, time.Minute, 5 * time.Minute, 15 * time.Minute}
	for i := 0; i < 4; i++ {
		hs.done(hs.fire(j.ID))
		got := hs.job(j.ID).State.Next.Sub(hs.now)
		if got != want[i] {
			t.Fatalf("failure %d: retry in %s; want %s", i+1, got, want[i])
		}
	}
	hs.done(hs.fire(j.ID))
	j = hs.job(j.ID)
	if j.Enabled || !strings.Contains(j.State.LastError, "paused after 5 failures") {
		t.Fatalf("after 5 failures: enabled=%v error=%q", j.Enabled, j.State.LastError)
	}
}

func TestPacingIsClampedAndStopEnds(t *testing.T) {
	hs := newHarness(t)
	j := hs.add(&Job{Name: "ci", Kind: KindTask, Prompt: "watch CI", Pacing: &Pacing{Min: "1m", Max: "1h"}})
	sess := hs.fire(j.ID)
	if !strings.Contains(hs.h.prompts[0], "schedule tool, action next") {
		t.Fatal("a paced run is not told how to pace")
	}
	if _, err := hs.s.Propose(sess, 10*time.Second, "build almost done", false); err != nil {
		t.Fatal(err)
	}
	hs.done(sess)
	if got := hs.job(j.ID).State.Next.Sub(hs.now); got != time.Minute {
		t.Fatalf("a 10s proposal gave %s; want the 1m floor", got)
	}
	sess = hs.fire(j.ID)
	hs.done(sess) // no proposal: the ceiling
	if got := hs.job(j.ID).State.Next.Sub(hs.now); got != time.Hour {
		t.Fatalf("no proposal gave %s; want 1h", got)
	}
	sess = hs.fire(j.ID)
	_, _ = hs.s.Propose(sess, 0, "", true)
	hs.done(sess)
	if j = hs.job(j.ID); j.Enabled {
		t.Fatal("stop did not end the loop")
	}
	if _, err := hs.s.Propose("not-a-run", time.Minute, "", false); err == nil {
		t.Fatal("a proposal outside a run was accepted")
	}
}

func TestHeartbeatSkipsAnEmptyChecklistAndGatesSayNo(t *testing.T) {
	hs := newHarness(t)
	root := t.TempDir()
	j := hs.add(&Job{Name: "hb", Kind: KindHeartbeat, Root: root, Every: "30m"})
	hs.fire(j.ID)
	if r := hs.s.Store.Runs(j.ID, 1)[0]; r.Status != StatusSkipped || !strings.Contains(r.Skipped, "empty") {
		t.Fatalf("run = %+v", r)
	}
	if len(hs.h.prompts) != 0 {
		t.Fatal("an empty checklist cost a turn")
	}
	_ = os.MkdirAll(filepath.Join(root, ".agent-tui"), 0o755)
	_ = os.WriteFile(ChecklistPath(root), []byte("# Heartbeat\n- [ ] check CI on main\n"), 0o644)
	hs.h.answer = "HEARTBEAT_OK"
	hs.done(hs.fire(j.ID))
	if !strings.Contains(hs.h.prompts[0], "check CI on main") || hs.job(j.ID).State.LastStatus != StatusSilent {
		t.Fatalf("heartbeat: prompt %q, status %s", hs.h.prompts[0], hs.job(j.ID).State.LastStatus)
	}

	g := hs.add(&Job{Name: "gated", Kind: KindTask, Prompt: "fix CI", Every: "10m", Gate: "gh pr checks"})
	hs.h.gateOK = false
	hs.fire(g.ID)
	if r := hs.s.Store.Runs(g.ID, 1)[0]; r.Skipped != "the gate said no" {
		t.Fatalf("gate no: %+v", r)
	}
	hs.h.gateOK = true
	hs.done(hs.fire(g.ID))
	if last := hs.h.prompts[len(hs.h.prompts)-1]; !strings.Contains(last, "2 checks failing") {
		t.Fatalf("the gate's output is not in the prompt: %q", last)
	}
}

func TestSameSessionWaitsForItsTurnAndOneShotsGo(t *testing.T) {
	hs := newHarness(t)
	j := hs.add(&Job{Name: "loop", Kind: KindTask, Prompt: "check deploy", Every: "5m", Session: SessionSame, SessionID: "mine"})
	hs.h.busy["mine"] = true
	hs.now = hs.job(j.ID).State.Next
	hs.s.Tick()
	time.Sleep(30 * time.Millisecond)
	if len(hs.h.prompts) != 0 {
		t.Fatal("it ran in a busy session")
	}
	hs.h.busy["mine"] = false
	if sess := hs.fire(j.ID); sess != "mine" {
		t.Fatalf("ran in %q; want the session it belongs to", sess)
	}

	at := hs.add(&Job{Name: "remind", Kind: KindTask, Prompt: "push the release", At: hs.now.Add(45 * time.Minute), DeleteAfterRun: true})
	hs.done(hs.fire(at.ID))
	if _, ok := hs.s.Store.Get(at.ID); ok {
		t.Fatal("a one-shot that deletes itself is still there")
	}
}

func TestRecoverFailsOrphansAndCatchesUpOnce(t *testing.T) {
	hs := newHarness(t)
	a := hs.add(&Job{Name: "a", Kind: KindTask, Prompt: "x", Every: "1h"})
	b := hs.add(&Job{Name: "b", Kind: KindTask, Prompt: "x", Every: "1h"})
	c := hs.add(&Job{Name: "c", Kind: KindTask, Prompt: "x", Every: "1h"})
	_, _ = hs.s.Store.Update(a.ID, func(j *Job) bool { j.State.Running, j.State.RunStarted = "s9", hs.now; return true })
	_, _ = hs.s.Store.Update(b.ID, func(j *Job) bool { j.State.Next = hs.now.Add(-3 * time.Hour); return true })
	_, _ = hs.s.Store.Update(c.ID, func(j *Job) bool { j.State.Next = hs.now.Add(-10 * 24 * time.Hour); return true })

	hs.s.started = hs.now
	hs.s.recover()
	if r := hs.s.Store.Runs(a.ID, 1)[0]; r.Status != StatusError || hs.job(a.ID).State.Running != "" {
		t.Fatalf("orphan: %+v", r)
	}
	if hs.job(c.ID).State.Next.Before(hs.now) {
		t.Fatal("a run missed ten days ago is still due")
	}
	hs.h.answer = "done"
	hs.done(hs.fire(b.ID))
	if r := hs.s.Store.Runs(b.ID, 1)[0]; r.Reason != "catch-up" {
		t.Fatalf("missed 3h ago: reason %q", r.Reason)
	}
	if n := len(hs.h.prompts); n != 1 {
		t.Fatalf("%d runs for one missed job; want one catch-up", n)
	}
}
