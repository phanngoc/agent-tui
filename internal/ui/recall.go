package ui

import (
	"context"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/phanngoc/agent-tui/internal/search"
	"github.com/phanngoc/agent-tui/internal/session"
)

// Finding the conversation you had about this three weeks ago.
//
// The transcript is the only record here that is complete, durable and neutral
// between engines, so it is the only thing worth searching — and searching it
// must not mean opening every conversation as a session. `/recall` therefore
// reads a narrow projection of every file in the store, from every project,
// and a hit is an address: a conversation, and a message in it.
//
// Only what was said. Tool results are eighty-five per cent of the bytes on
// disk and are file dumps and JSON; the answer to "where did I ask about this"
// is in the prose, which is eight per cent.

// Two caps, not one. The first bounds the list, the second bounds any single
// conversation in it — grep needs no per-file cap because source files are
// naturally small, but a forty-kilobyte conversation is not, and without this
// one long session crowds every other project off the screen.
const (
	recallLimit   = 300
	recallPerConv = 20
)

// convoHit is one matching line plus the address the transcript needs.
//
// It wraps a search.Match rather than overloading one. Match carries the line,
// the match within it and the columns, which is exactly what the row renderer
// wants; but Match.Line means "line number" everywhere else in this program,
// and grepHit prints it into a line-number gutter. A message ordinal shown in
// that column would be a lie told to the reader.
type convoHit struct {
	search.Match
	conv int    // index into the result's conversations
	msg  int    // index into that conversation's messages — what the jump uses
	role string // who said it, for the row's tag
}

// convoConv is one conversation with hits in it, as the list groups them.
type convoConv struct {
	entry     session.Entry
	hits      []convoHit
	collapsed bool
}

// convoRow is one line of the list: a conversation header, or a hit under it.
type convoRow struct {
	conv int
	hit  int // -1 for the header
}

// convoResult is what one scan produced.
type convoResult struct {
	convs []convoConv
	total int
	err   error
}

// recallMsg carries a corpus read or a scan back to the update loop, tagged
// with the sequence it belongs to so a stale one is dropped.
type recallMsg struct {
	seq     int
	corpus  []session.Entry
	loadErr error
}

// openRecall starts a search over every conversation.
func (m *Model) openRecall(query string) tea.Cmd {
	m.overlay = overlayRecall
	m.recallIn.SetValue(query)
	m.recallIn.Focus()
	m.recallIn.CursorEnd()
	m.recallRes = convoResult{}
	m.recallRows, m.recallSel, m.recallTop = nil, 0, 0
	m.recallPending = query
	return m.loadCorpus()
}

// loadCorpus reads every conversation once, when the overlay opens.
//
// The open sessions are snapshotted here, on this goroutine, because nothing
// else may read Session.Messages while a turn is appending to one. The disk
// read is what goes into the command, and it skips the ids already taken: an
// open conversation may hold messages the disk has never seen.
//
// Asynchronous not for the eleven milliseconds it costs today but for the
// hundreds it costs at two hundred conversations — which would land on exactly
// the frame the overlay opens.
func (m *Model) loadCorpus() tea.Cmd {
	if m.recallStop != nil {
		m.recallStop()
	}
	m.recallSeq++
	seq := m.recallSeq
	m.recallBusy = true

	live := m.mgr.LiveEntries()
	skip := make(map[string]bool, len(live))
	for _, e := range live {
		skip[e.ID] = true
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.recallStop = cancel
	mgr := m.mgr
	return func() tea.Msg {
		defer cancel()
		return recallMsg{seq: seq, corpus: append(live, mgr.DiskEntries(ctx, skip)...)}
	}
}

// setCorpus takes the loaded conversations and runs whatever was typed while
// they were arriving.
func (m *Model) setCorpus(msg recallMsg) {
	m.recallBusy = false
	m.corpus = msg.corpus
	if q := m.recallPending; q != "" {
		m.recallPending = ""
		m.runRecall(q)
	}
}

// runRecall scans the corpus already in memory. Synchronous, like the file
// finder and unlike the project grep: this is a third of a megabyte of prose,
// not a repository.
func (m *Model) runRecall(query string) {
	m.recallRes = scanCorpus(m.corpus, search.Options{
		Query:         query,
		Regex:         m.recallRegex,
		CaseSensitive: m.recallCase,
		Limit:         recallLimit,
	})
	m.rebuildRecallRows()
}

// scanCorpus searches the prose of every conversation.
//
// Pure: conversations in, results out, nothing here touches the Model — so it
// is testable without a terminal and cannot accidentally read a session while
// a turn is writing to one.
func scanCorpus(entries []session.Entry, o search.Options) convoResult {
	if strings.TrimSpace(o.Query) == "" {
		return convoResult{}
	}
	mt, err := search.Compile(o)
	if err != nil {
		return convoResult{err: err}
	}
	limit := o.Limit
	if limit <= 0 {
		limit = recallLimit
	}

	var out convoResult
	for i := range entries {
		e := &entries[i]
		conv := convoConv{entry: *e}
		for msg, text := range e.Texts {
			if text == "" {
				continue
			}
			for _, hit := range mt.Scan(e.ID, []byte(text)) {
				conv.hits = append(conv.hits, convoHit{
					Match: hit, conv: len(out.convs), msg: msg, role: e.Roles[msg],
				})
				if len(conv.hits) >= recallPerConv {
					break
				}
			}
			if len(conv.hits) >= recallPerConv {
				break
			}
		}
		if len(conv.hits) == 0 {
			continue
		}
		out.convs = append(out.convs, conv)
		out.total += len(conv.hits)
		if out.total >= limit {
			break
		}
	}
	return out
}

// rebuildRecallRows flattens the groups into the lines the list draws.
func (m *Model) rebuildRecallRows() {
	m.recallRows = m.recallRows[:0]
	for i := range m.recallRes.convs {
		m.recallRows = append(m.recallRows, convoRow{conv: i, hit: -1})
		if m.recallRes.convs[i].collapsed {
			continue
		}
		for j := range m.recallRes.convs[i].hits {
			m.recallRows = append(m.recallRows, convoRow{conv: i, hit: j})
		}
	}
	m.recallSel = clampRow(m.recallSel, len(m.recallRows))
}

// selectedRecall is the hit under the cursor, if the cursor is on one.
func (m *Model) selectedRecall() (convoHit, session.Entry, bool) {
	if m.recallSel < 0 || m.recallSel >= len(m.recallRows) {
		return convoHit{}, session.Entry{}, false
	}
	r := m.recallRows[m.recallSel]
	c := m.recallRes.convs[r.conv]
	if r.hit < 0 {
		return convoHit{}, c.entry, false
	}
	return c.hits[r.hit], c.entry, true
}

// openRecallHit makes that conversation active and lands on the message the
// match was found in.
//
// The conversation may belong to another project and may not be loaded at all.
// Restore cannot help: it reads every file, keeps only one project's, caps at a
// limit and replaces the whole list. Open reads the one that was asked for.
func (m *Model) openRecallHit(hit convoHit, e session.Entry) tea.Cmd {
	m.closeOverlay()
	s, i, err := m.mgr.Open(e.ID)
	if err != nil {
		m.notice = "that conversation is no longer on disk"
		return nil
	}
	foreign := s.Root != m.cfg.Root
	m.mgr.Select(i)
	cmd := m.onSessionSwitchAt(hit.msg)

	// Opening a conversation from elsewhere retargets the file tree and the
	// index, which is right — the transcript is about that project's files —
	// and is also a visible, several-hundred-millisecond event. Say so rather
	// than let it look like a glitch.
	if foreign {
		m.notice = "opened " + s.Label() + " — from " + filepath.Base(s.Root)
	}
	return cmd
}

// ---- keys ------------------------------------------------------------------

func (m *Model) recallKey(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		m.closeOverlay()
		return nil
	case "enter", "right":
		if hit, e, ok := m.selectedRecall(); ok {
			return m.openRecallHit(hit, e)
		}
		m.foldRecall(false)
		return nil
	case "left":
		m.foldRecall(true)
		return nil
	case "down", "ctrl+n":
		m.recallSel = clampRow(m.recallSel+1, len(m.recallRows))
		return nil
	case "up", "ctrl+p":
		m.recallSel = clampRow(m.recallSel-1, len(m.recallRows))
		return nil
	case "alt+a":
		m.recallCase = !m.recallCase
		m.runRecall(m.recallIn.Value())
		return nil
	case "alt+r":
		m.recallRegex = !m.recallRegex
		m.runRecall(m.recallIn.Value())
		return nil
	}

	before := m.recallIn.Value()
	var cmd tea.Cmd
	m.recallIn, cmd = m.recallIn.Update(k)
	if q := m.recallIn.Value(); q != before {
		if m.recallBusy {
			// The corpus is still arriving. Remember the query rather than
			// search an empty one and show "no results" for a conversation
			// that is simply not read yet.
			m.recallPending = q
		} else if len(q) >= 2 {
			m.runRecall(q)
		} else {
			m.recallRes, m.recallRows = convoResult{}, nil
		}
	}
	return cmd
}

// clampRow keeps a cursor inside a list of n rows.
func clampRow(i, n int) int {
	if i < 0 || n == 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}

// foldRecall opens or closes the conversation the cursor is in.
func (m *Model) foldRecall(shut bool) {
	if m.recallSel < 0 || m.recallSel >= len(m.recallRows) {
		return
	}
	i := m.recallRows[m.recallSel].conv
	m.recallRes.convs[i].collapsed = shut
	m.rebuildRecallRows()
	// Land on the header, so closing a conversation does not leave the cursor
	// pointing at a row that is no longer there.
	for j, r := range m.recallRows {
		if r.conv == i && r.hit == -1 {
			m.recallSel = j
			break
		}
	}
}
