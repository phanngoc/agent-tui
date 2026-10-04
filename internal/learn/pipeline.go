package learn

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/phanngoc/agent-tui/internal/memory"
	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/skill"
)

// Limits, TencentDB's defaults unless noted.
const (
	batchSize     = 10 // new messages per L1 run
	background    = 5  // earlier messages shown for context
	maxPerBatch   = 20 // memories kept from one run
	candidatesTop = 5  // existing records shown to the dedup judge per memory
	maxScenes     = 15
	messageCap    = 3000 // characters of one message shown to the extractor
	// personaEvery is how many new memories trigger a persona rewrite. Lower
	// than TencentDB's 50: one person's coding sessions produce far fewer
	// memories than a chat assistant's.
	personaEvery = 20
)

var codeBlockRE = regexp.MustCompile("(?s)```.*?```")

// msg is one message as the extractor sees it.
type msg struct {
	ID   string
	Role string
	At   time.Time
	Text string
}

func render(ms []msg) string {
	if len(ms) == 0 {
		return "(none)"
	}
	var b strings.Builder
	for i, m := range ms {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "[%s] [%s] [%s]: %s", m.ID, m.Role, m.At.UTC().Format(time.RFC3339), m.Text)
	}
	return b.String()
}

// toMsgs keeps what L0 capture keeps: user and assistant text, without code
// blocks in the assistant's (the code is in the repository; what is worth
// remembering is what was said about it) and without slash commands.
func toMsgs(all []session.Message, from, to int) []msg {
	var out []msg
	for i := from; i < to && i < len(all); i++ {
		m := all[i]
		text := strings.TrimSpace(m.Text)
		if m.Role == session.RoleAssistant {
			text = strings.TrimSpace(codeBlockRE.ReplaceAllString(text, "[code]"))
			if len(m.Tools) > 0 {
				names := make([]string, 0, len(m.Tools))
				for _, t := range m.Tools {
					names = append(names, t.Name)
				}
				text += "\n(tools used: " + strings.Join(names, ", ") + ")"
			}
		}
		if text == "" || strings.HasPrefix(text, "/") || m.Shell != nil {
			continue
		}
		if !hasWord(text) {
			continue
		}
		if len(text) > messageCap {
			text = text[:messageCap] + " …"
		}
		out = append(out, msg{ID: fmt.Sprintf("m%d", i), Role: m.Role, At: m.At, Text: text})
	}
	return out
}

func hasWord(s string) bool { return len(memory.Tokens(s)) > 0 }

// extracted is one memory from the extraction answer.
type extracted struct {
	Content  string         `json:"content"`
	Type     string         `json:"type"`
	Priority *int           `json:"priority"`
	Scope    string         `json:"scope"`
	Sources  []string       `json:"source_message_ids"`
	Metadata map[string]any `json:"metadata"`
	scene    string
}

type sceneOut struct {
	Name     string      `json:"scene_name"`
	Memories []extracted `json:"memories"`
}

// floor is the lowest priority a type keeps.
func floor(t string) int {
	switch t {
	case memory.TypePersona:
		return 50
	case memory.TypeEpisodic:
		return 60
	}
	return 70
}

// extract runs L1 extraction over newMsgs.
func (l *Learner) extract(ctx context.Context, prevScene string, bg, newMsgs []msg) ([]extracted, string, error) {
	if prevScene == "" {
		prevScene = "(none)"
	}
	user := "[Previous scene]: " + prevScene +
		"\n\n[Background messages] (context only, NEVER extract from these):\n" + render(bg) +
		"\n\n━━━━━━━━\n\n[New messages to extract] (use the timestamps for absolute dates):\n" + render(newMsgs)
	text, err := l.llm.Complete(ctx, extractSystem, user, 4096)
	if err != nil {
		return nil, prevScene, err
	}
	var scenes []sceneOut
	if err := decodeJSON(text, &scenes); err != nil {
		return nil, prevScene, err
	}
	var out []extracted
	last := prevScene
	for _, sc := range scenes {
		if sc.Name != "" {
			last = sc.Name
		}
		for _, m := range sc.Memories {
			m.Content = strings.TrimSpace(m.Content)
			if m.Content == "" || !memory.ValidType(m.Type) {
				continue
			}
			p := 50
			if m.Priority != nil {
				p = *m.Priority
			}
			if p >= 0 && p < floor(m.Type) {
				continue
			}
			m.Priority = &p
			m.scene = sc.Name
			out = append(out, m)
		}
	}
	if len(out) > maxPerBatch {
		out = out[:maxPerBatch]
	}
	return out, last, nil
}

func (m extracted) scopeOf() memory.Scope {
	switch memory.Scope(m.Scope) {
	case memory.Global, memory.Project:
		return memory.Scope(m.Scope)
	}
	return memory.DefaultScope(m.Type)
}

func sourceIdx(ids []string) []int {
	var out []int
	for _, id := range ids {
		var n int
		if _, err := fmt.Sscanf(id, "m%d", &n); err == nil {
			out = append(out, n)
		}
	}
	return out
}

type decision struct {
	RecordID   string   `json:"record_id"`
	Action     string   `json:"action"`
	Targets    []string `json:"target_ids"`
	Content    string   `json:"merged_content"`
	Type       string   `json:"merged_type"`
	Priority   *int     `json:"merged_priority"`
	Timestamps []string `json:"merged_timestamps"`
}

// Outcome counts what a run did.
type Outcome struct {
	Stored, Updated, Merged, Skipped int
	Records                          []memory.Record
}

// consolidate dedups new memories against one store and writes them.
// Anything that goes wrong with the judge falls back to storing everything:
// a duplicate can be merged later, a lost memory cannot be recovered.
func (l *Learner) consolidate(ctx context.Context, st *memory.Store, sessionID string, mems []extracted) Outcome {
	var out Outcome
	existing := st.All()
	cands := make([][]memory.Hit, len(mems))
	any := false
	for i, m := range mems {
		cands[i] = memory.Search(existing, m.Content, candidatesTop)
		any = any || len(cands[i]) > 0
	}

	now := time.Now().UTC()
	toRecord := func(m extracted) memory.Record {
		return memory.Record{
			Content: m.Content, Type: m.Type, Priority: *m.Priority, Scene: m.scene,
			Session: sessionID, Sources: sourceIdx(m.Sources), Metadata: m.Metadata,
			Timestamps: []time.Time{now}, Origin: "learned",
		}
	}
	storeAll := func(from int) {
		for _, m := range mems[from:] {
			if r, err := st.Put(toRecord(m)); err == nil {
				out.Stored++
				out.Records = append(out.Records, r)
			}
		}
	}
	if !any {
		storeAll(0)
		return out
	}

	// One call for the whole batch, with a shared pool of candidates.
	pool := map[string]memory.Record{}
	var b strings.Builder
	b.WriteString("## Existing records\n")
	var ids []string
	for _, hs := range cands {
		for _, h := range hs {
			if _, seen := pool[h.Record.ID]; !seen {
				pool[h.Record.ID] = h.Record
				ids = append(ids, h.Record.ID)
			}
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		r := pool[id]
		ts := make([]string, 0, len(r.Timestamps))
		for _, t := range r.Timestamps {
			ts = append(ts, t.Format(time.RFC3339))
		}
		fmt.Fprintf(&b, "- record_id=%s type=%s priority=%d scene=%q timestamps=%v\n  %s\n",
			r.ID, r.Type, r.Priority, r.Scene, ts, r.Content)
	}
	b.WriteString("\n## New memories\n")
	for i, m := range mems {
		var rel []string
		for _, h := range cands[i] {
			rel = append(rel, h.Record.ID)
		}
		fmt.Fprintf(&b, "- record_id=new_%d type=%s priority=%d related=%v\n  %s\n",
			i, m.Type, *m.Priority, rel, m.Content)
	}

	text, err := l.llm.Complete(ctx, dedupSystem, b.String(), 4096)
	var ds []decision
	if err == nil {
		err = decodeJSON(text, &ds)
	}
	if err != nil {
		l.note("dedup fell back to storing all: " + err.Error())
		storeAll(0)
		return out
	}
	byID := map[string]decision{}
	for _, d := range ds {
		byID[d.RecordID] = d
	}
	replaced := map[string]bool{}
	for i, m := range mems {
		d, ok := byID[fmt.Sprintf("new_%d", i)]
		if !ok {
			d.Action = "store"
		}
		var targets []string
		for _, t := range d.Targets {
			if _, known := pool[t]; known && !replaced[t] {
				targets = append(targets, t)
			}
		}
		switch d.Action {
		case "skip":
			out.Skipped++
		case "update", "merge":
			if len(targets) == 0 {
				d.Action = "store"
				break
			}
			r := toRecord(m)
			if c := strings.TrimSpace(d.Content); c != "" {
				r.Content = c
			}
			if memory.ValidType(d.Type) {
				r.Type = d.Type
			}
			if d.Priority != nil {
				r.Priority = *d.Priority
			}
			r.Timestamps = mergeTimes(pool, targets, d.Timestamps, now)
			if got, err := st.Replace(targets, r, d.Action); err == nil {
				for _, t := range targets {
					replaced[t] = true
				}
				out.Records = append(out.Records, got)
				if d.Action == "update" {
					out.Updated++
				} else {
					out.Merged++
				}
			}
			continue
		}
		if d.Action != "skip" {
			if r, err := st.Put(toRecord(m)); err == nil {
				out.Stored++
				out.Records = append(out.Records, r)
			}
		}
	}
	return out
}

func mergeTimes(pool map[string]memory.Record, targets, given []string, now time.Time) []time.Time {
	seen := map[int64]bool{}
	var out []time.Time
	add := func(t time.Time) {
		if t.IsZero() || seen[t.Unix()] {
			return
		}
		seen[t.Unix()] = true
		out = append(out, t)
	}
	for _, id := range targets {
		for _, t := range pool[id].Timestamps {
			add(t)
		}
	}
	for _, s := range given {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			add(t)
		}
	}
	add(now)
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

type sceneOp struct {
	Action    string   `json:"action"`
	File      string   `json:"file"`
	MergeFrom []string `json:"merge_from"`
	Summary   string   `json:"summary"`
	Body      string   `json:"body"`
}

// scenes is L2: fold new records into the scene blocks of one store. It
// reports whether the persona should be rewritten.
func (l *Learner) scenes(ctx context.Context, st *memory.Store, recs []memory.Record) (bool, error) {
	if len(recs) == 0 {
		return false, nil
	}
	existing := st.Scenes()
	var newText strings.Builder
	for _, r := range recs {
		fmt.Fprintf(&newText, "- [%s|%s] %s (id %s, %s)\n", r.Type, r.Scene, r.Content, r.ID, r.Updated.Format("2006-01-02"))
	}

	// The blocks most like the new memories are shown in full; the rest by
	// summary only.
	docs := make([]memory.Record, len(existing))
	for i, sc := range existing {
		docs[i] = memory.Record{ID: sc.File, Content: sc.Summary + "\n" + sc.Body}
	}
	related := memory.Search(docs, newText.String(), 3)
	full := map[string]bool{}
	for _, h := range related {
		full[h.Record.ID] = true
	}

	var u strings.Builder
	fmt.Fprintf(&u, "Current time: %s\nScene count: %d of at most %d\n\n## New memories\n%s\n## Existing blocks\n",
		time.Now().UTC().Format(time.RFC3339), len(existing), maxScenes, newText.String())
	if len(existing) == 0 {
		u.WriteString("(none yet)\n")
	}
	for _, sc := range existing {
		fmt.Fprintf(&u, "- %s (heat %d): %s\n", sc.File, sc.Heat, sc.Summary)
	}
	for _, sc := range existing {
		if full[sc.File] {
			fmt.Fprintf(&u, "\n### Full text of %s\n%s\n", sc.File, sc.Body)
		}
	}

	limit := ""
	switch n := len(existing); {
	case n >= maxScenes:
		limit = "\nThe block count is at the cap: you MUST merge blocks before anything else, and you may not create."
	case n == maxScenes-1:
		limit = "\nOne below the cap: you may not create a block."
	case n >= maxScenes-3:
		limit = "\nClose to the cap: prefer update or merge."
	}
	text, err := l.llm.Complete(ctx, fmt.Sprintf(sceneSystem, maxScenes, limit), u.String(), 8192)
	if err != nil {
		return false, err
	}
	var ans struct {
		Ops     []sceneOp `json:"operations"`
		Persona string    `json:"persona_update"`
	}
	if err := decodeJSON(text, &ans); err != nil {
		return false, err
	}
	heat := map[string]int{}
	for _, sc := range existing {
		heat[sc.File] = sc.Heat
	}
	created := 0
	for _, op := range ans.Ops {
		if strings.TrimSpace(op.Body) == "" {
			continue
		}
		file := memory.SceneFile(op.File)
		h := 1
		switch op.Action {
		case "update":
			h = heat[file] + 1
		case "merge":
			h = heat[file] + 1
			for _, f := range op.MergeFrom {
				f = memory.SceneFile(f)
				if f == file {
					continue
				}
				h += heat[f]
				_ = st.DeleteScene(f)
			}
		case "create":
			if created > 0 || len(existing) >= maxScenes-1 {
				continue
			}
			created++
		default:
			continue
		}
		if _, err := st.PutScene(memory.Scene{File: file, Summary: op.Summary, Heat: h, Body: op.Body}); err != nil {
			return false, err
		}
	}
	return strings.TrimSpace(ans.Persona) != "", nil
}

// persona is L3: rewrite the persona (global) or the doctrine (project).
func (l *Learner) persona(ctx context.Context, st *memory.Store) error {
	scenes := st.Scenes()
	if len(scenes) == 0 {
		return nil
	}
	what, ask, limit := "user persona", personaUser, 2000
	if st.Scope == memory.Project {
		what, ask, limit = "project operating doctrine", doctrineUser, 1200
	}
	var u strings.Builder
	if old := st.Persona(); old != "" {
		u.WriteString("## Existing version\n" + old + "\n\n")
	}
	u.WriteString("## Scene blocks\n")
	for _, sc := range scenes {
		fmt.Fprintf(&u, "### %s (heat %d)\n%s\n\n", sc.File, sc.Heat, sc.Body)
	}
	text, err := l.llm.Complete(ctx, fmt.Sprintf(personaSystem, what, ask, limit), u.String(), 4096)
	if err != nil {
		return err
	}
	text = strings.TrimSpace(thinkRE.ReplaceAllString(text, ""))
	if text == "" {
		return nil
	}
	if r := []rune(text); len(r) > limit+400 {
		text = string(r[:limit+400])
	}
	return st.SetPersona(text)
}

// reviewSkill is skill learning: read a transcript with its tool calls and
// create or improve a skill when the work taught a reusable procedure.
func (l *Learner) reviewSkill(ctx context.Context, store skill.Store, all []session.Message, from int) (string, error) {
	var t strings.Builder
	for i := from; i < len(all); i++ {
		m := all[i]
		tag := "past-user"
		if m.Role == session.RoleAssistant {
			tag = "past-assistant"
		}
		fmt.Fprintf(&t, "<<%s>>\n%s\n", tag, strings.TrimSpace(m.Text))
		for _, c := range m.Tools {
			fmt.Fprintf(&t, "[tool %s] %s\n→ %s\n", c.Name, clip(string(c.Input), 600), headTail(c.Result, 2048))
		}
		fmt.Fprintf(&t, "<<end-%s>>\n", tag)
	}
	tr := t.String()
	if len(tr) > 40000 {
		tr = tr[:8000] + "\n…[middle of the transcript omitted]…\n" + tr[len(tr)-32000:]
	}

	skills := store.List()
	docs := make([]memory.Record, len(skills))
	for i, s := range skills {
		docs[i] = memory.Record{ID: s.Name, Content: s.Name + " " + s.Description}
	}
	near := map[string]bool{}
	for _, h := range memory.Search(docs, tr, 3) {
		near[h.Record.ID] = true
	}
	var u strings.Builder
	u.WriteString("## Existing skills\n")
	if len(skills) == 0 {
		u.WriteString("(none)\n")
	}
	for _, s := range skills {
		fmt.Fprintf(&u, "- %s [%s]: %s\n", s.Name, s.Scope, s.Description)
	}
	for _, s := range skills {
		if near[s.Name] && !s.Shadowed {
			fmt.Fprintf(&u, "\n### Current body of %s\n%s\n", s.Name, s.Body)
		}
	}
	u.WriteString("\n## Transcript\n" + tr)

	text, err := l.llm.Complete(ctx, skillSystem, u.String(), 8192)
	if err != nil {
		return "", err
	}
	var ans struct {
		Action      string `json:"action"`
		Name        string `json:"name"`
		Scope       string `json:"scope"`
		Description string `json:"description"`
		Body        string `json:"body"`
		Reason      string `json:"reason"`
	}
	if err := decodeJSON(text, &ans); err != nil {
		return "", err
	}
	if ans.Action != "create" && ans.Action != "update" {
		return "nothing to save: " + ans.Reason, nil
	}
	scope := skill.Project
	if ans.Scope == "global" || store.ProjectDir == "" {
		scope = skill.Global
	}
	if old, ok := store.Get(ans.Name); ok && ans.Action == "update" {
		scope = old.Scope
	}
	sk, err := store.Save(skill.Skill{Name: ans.Name, Description: ans.Description,
		Body: ans.Body, Scope: scope, Learned: true})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s %s skill %s: %s", ans.Action+"d", sk.Scope, sk.Name, ans.Reason), nil
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func headTail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n/2] + "\n…\n" + s[len(s)-n/2:]
}
