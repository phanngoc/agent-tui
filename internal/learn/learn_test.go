package learn

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/memory"
	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/skill"
)

// fake answers each stage by recognising its system prompt.
type fake struct {
	calls []string
	dedup string
}

func (f *fake) Name() string { return "fake" }

func (f *fake) Complete(_ context.Context, system, user string, _ int64) (string, error) {
	switch {
	case strings.HasPrefix(system, "You are an expert at segmenting"):
		f.calls = append(f.calls, "extract")
		return "```json\n" + `[{"scene_name":"Agent fixing search ranking","message_ids":["m0","m1"],"memories":[
			{"content":"The project ranks search results by path depth","type":"work_fact","priority":85,"scope":"project","source_message_ids":["m1"]},
			{"content":"The user wants answers in Vietnamese","type":"persona","priority":90,"source_message_ids":["m0"]},
			{"content":"noise","type":"work_fact","priority":20}]}]` + "\n```", nil
	case strings.HasPrefix(system, "You are the memory consolidation judge"):
		f.calls = append(f.calls, "dedup")
		return f.dedup, nil
	case strings.HasPrefix(system, "You maintain the scene blocks"):
		f.calls = append(f.calls, "scenes")
		return `{"operations":[{"action":"create","file":"search.md","summary":"search ranking","body":"## Key facts\n- depth"}],"persona_update":"new"}`, nil
	case strings.HasPrefix(system, "You write the"):
		f.calls = append(f.calls, "persona")
		return "## Archetype\nA careful engineer", nil
	case strings.HasPrefix(system, "You are a Skill Review agent"):
		f.calls = append(f.calls, "skill")
		return `{"action":"create","name":"fix-ranking","scope":"project","description":"How to fix ranking bugs","body":"1. grep score","reason":"repeatable"}`, nil
	}
	return "", nil
}

func setup(t *testing.T) (*Learner, *fake, string) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	l := New(t.TempDir())
	f := &fake{}
	l.llm = f
	return l, f, t.TempDir()
}

func transcript(tools int) []session.Message {
	now := time.Now()
	a := session.Message{Role: session.RoleAssistant, Text: "Fixed: ranking now uses path depth.\n```go\ncode\n```", At: now}
	for i := 0; i < tools; i++ {
		a.Tools = append(a.Tools, session.ToolCall{ID: "t", Name: "grep", Input: []byte(`{"query":"score"}`), Result: "rank.go:10"})
	}
	return []session.Message{
		{Role: session.RoleUser, Text: "trả lời bằng tiếng Việt, sửa ranking giúp tôi", At: now},
		a,
	}
}

func TestPipelineExtractsScenesPersonaAndSkill(t *testing.T) {
	l, f, root := setup(t)
	if err := l.run(job{session: "s1", root: root, msgs: transcript(SkillToolCalls), force: true}); err != nil {
		t.Fatal(err)
	}
	bank := memory.For(root)
	proj, glob := bank.Project.All(), bank.Global.All()
	if len(proj) != 1 || proj[0].Scope != memory.Project || proj[0].Session != "s1" || proj[0].Sources[0] != 1 {
		t.Fatalf("project memory: %+v", proj)
	}
	if len(glob) != 1 || glob[0].Type != memory.TypePersona {
		t.Fatalf("global memory: %+v", glob)
	}
	// Nothing existed, so no judge was needed; the first run consolidates at
	// once, and a store with scenes and no persona gets one.
	got := strings.Join(f.calls, ",")
	if got != "extract,scenes,persona,scenes,persona,skill" {
		t.Fatalf("stages: %s", got)
	}
	if len(bank.Project.Scenes()) != 1 || bank.Project.Persona() == "" {
		t.Fatal("no scene or doctrine")
	}
	sk, ok := skill.For(root).Get("fix-ranking")
	if !ok || !sk.Learned || sk.Scope != skill.Project {
		t.Fatalf("skill: %+v %v", sk, ok)
	}
	st := l.Status().Sessions["s1"]
	if st.Cursor != 2 || st.Threshold != 2 || st.SkillCursor != 2 {
		t.Fatalf("bookmark: %+v", st)
	}
	acts := l.Activities(20)
	if len(acts) == 0 || acts[len(acts)-1].Stage != "extract" || len(acts[len(acts)-1].Records) == 0 {
		t.Fatalf("activity: %+v", acts)
	}
}

func TestDedupMergesIntoExistingRecords(t *testing.T) {
	l, f, root := setup(t)
	bank := memory.For(root)
	old, _ := bank.Project.Put(memory.Record{Content: "Search results are ranked by path depth", Type: memory.TypeWorkFact, Priority: 70})
	f.dedup = `[{"record_id":"new_0","action":"merge","target_ids":["` + old.ID + `","mem_unknown"],
		"merged_content":"The project ranks search results by path depth, shorter first","merged_type":"work_fact","merged_priority":82}]`
	if err := l.run(job{session: "s2", root: root, msgs: transcript(0), force: true}); err != nil {
		t.Fatal(err)
	}
	recs := bank.Project.All()
	if len(recs) != 1 || recs[0].Version != 2 || recs[0].Priority != 82 || !strings.Contains(recs[0].Content, "shorter first") {
		t.Fatalf("records: %+v", recs)
	}
	if !strings.Contains(strings.Join(f.calls, ","), "dedup") {
		t.Fatal("the judge was not asked")
	}
}

func TestBadJudgeAnswerStoresEverything(t *testing.T) {
	l, f, root := setup(t)
	bank := memory.For(root)
	_, _ = bank.Project.Put(memory.Record{Content: "ranks search results by path depth", Type: memory.TypeWorkFact, Priority: 70})
	f.dedup = "I could not decide"
	if err := l.run(job{session: "s3", root: root, msgs: transcript(0), force: true}); err != nil {
		t.Fatal(err)
	}
	if n := len(bank.Project.All()); n != 2 {
		t.Fatalf("%d records", n)
	}
}

func TestNotifyWarmsUp(t *testing.T) {
	l, _, root := setup(t)
	l.Notify(root, "s4", transcript(0)) // threshold 1: due at once
	select {
	case j := <-l.jobs:
		if j.session != "s4" {
			t.Fatal(j.session)
		}
	default:
		t.Fatal("the first turn was not queued")
	}
	l.mu.Lock()
	l.st.Sessions["s4"].Turns, l.st.Sessions["s4"].Threshold = 0, 4
	l.mu.Unlock()
	l.Notify(root, "s4", transcript(0))
	select {
	case <-l.jobs:
		t.Fatal("queued before the threshold")
	default:
	}
}

func TestDecodeJSONForgivesWrapping(t *testing.T) {
	var v []int
	if err := decodeJSON("<think>hmm</think>Here you go: [1,2,3] done", &v); err != nil || len(v) != 3 {
		t.Fatalf("%v %v", v, err)
	}
}

func TestDecodeJSONReadsWhatModelsActuallyWrite(t *testing.T) {
	// The judge's answer from a real log: valid JSON, but "" where a number
	// and a list belong, which used to throw the whole decision away.
	var ds []decision
	if err := decodeJSON(`[{"record_id":"new_0","action":"store","target_ids":[],"merged_content":"","merged_type":"","merged_priority":"","merged_timestamps":""}]`, &ds); err != nil {
		t.Fatal(err)
	}
	if len(ds) != 1 || ds[0].Action != "store" || ds[0].Priority.Set {
		t.Fatalf("%+v", ds)
	}
	var withNum []decision
	if err := decodeJSON(`[{"record_id":"new_0","action":"merge","target_ids":"mem_1","merged_priority":"85"}]`, &withNum); err != nil || withNum[0].Priority.V != 85 || len(withNum[0].Targets) != 1 {
		t.Fatalf("%+v %v", withNum, err)
	}

	// A Markdown body written with real line breaks inside the string.
	var scene struct {
		Ops []sceneOp `json:"operations"`
	}
	raw := "{\"operations\":[{\"action\":\"create\",\"file\":\"a.md\",\"body\":\"## Key facts\n- one\n- two\"}]}"
	if err := decodeJSON(raw, &scene); err != nil || len(scene.Ops) != 1 || scene.Ops[0].Body != "## Key facts\n- one\n- two" {
		t.Fatalf("%+v %v", scene, err)
	}

	// An answer cut off before it closed.
	var cut struct {
		Ops []sceneOp `json:"operations"`
	}
	if err := decodeJSON(`{"operations":[{"action":"update","file":"b.md","summary":"half writ`, &cut); err != nil || len(cut.Ops) != 1 || cut.Ops[0].File != "b.md" {
		t.Fatalf("%+v %v", cut, err)
	}
}

type flaky struct{ n int }

func (f *flaky) Name() string { return "flaky" }
func (f *flaky) Complete(_ context.Context, _, user string, _ int64) (string, error) {
	f.n++
	if f.n == 1 {
		return "Sure! Here it is: not json at all", nil
	}
	if !strings.Contains(user, "could not be read as JSON") {
		return "", nil
	}
	return `{"action":"none","reason":"ok"}`, nil
}

func TestAskRetriesOnceWithTheReason(t *testing.T) {
	// Isolated, or the user's own learn_model would pick a real model over
	// the one injected here.
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	l := New(t.TempDir())
	f := &flaky{}
	l.llm = f
	var v struct{ Action string }
	if err := l.ask(context.Background(), "sys", "user", 100, &v); err != nil || v.Action != "none" || f.n != 2 {
		t.Fatalf("%+v %v calls=%d", v, err, f.n)
	}
}

func TestConsolidateReportsEachStore(t *testing.T) {
	l, _, root := setup(t)
	bank := memory.For(root)
	_, _ = bank.Project.Put(memory.Record{Content: "The project ranks by depth", Type: memory.TypeWorkFact, Priority: 80})
	go l.loop()
	reps, err := l.Consolidate(context.Background(), bank.Stores())
	if err != nil || len(reps) != 2 {
		t.Fatalf("%+v %v", reps, err)
	}
	if reps[0].Note != "no memories yet" || reps[1].Folded != 1 || reps[1].ScenesAfter != 1 || !reps[1].Persona {
		t.Fatalf("reports: %+v", reps)
	}
}

// The periodic consolidation asks the model without a job having picked it
// first. That was a nil pointer that took the gateway down; with no model to
// be had, it is now an error.
func TestAskWithoutAPickedModelIsAnErrorNotACrash(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("PATH", t.TempDir()) // no claude CLI either
	l := New(t.TempDir())
	var v struct{}
	err := l.ask(context.Background(), "sys", "user", 100, &v)
	if err == nil {
		t.Skip("a model was found on this machine after all")
	}
}
