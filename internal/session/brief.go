package session

import (
	"strconv"
	"strings"
)

// Telling one agent what another one already did.
//
// A conversation here is three different memories wearing one name: this
// transcript, which belongs to the session; each engine's own conversation on
// its own server, which belongs to that engine's turn at holding it; and the
// reasoning context in this process, which belongs to the process. Only the
// first is ours, complete, and readable — so it is the only thing a handoff can
// be made of.
//
// A handoff is then one operation: bring the engine about to run from what it
// last saw up to where the conversation now is. There are two ways to carry
// that gap. agent.Replay renders it as SDK messages, for the engine that is
// handed the transcript itself; this renders it as text, for the three that get
// one string and nothing else.

// BriefLimit is how much of a transcript a handoff may carry, in bytes.
//
// It is a command-line argument on the other side: a CLI engine receives its
// whole prompt as one argv entry, and on Windows the entire command line has to
// fit in 32,767 characters — which a session aimed at WSL spends twice over,
// because the argv is re-quoted into a single `bash -lc` string. A brief that
// runs past that does not degrade; the process fails to start, with an error
// about nothing the reader did.
//
// It is also what the next engine has to read before it can answer. Real
// transcripts here run to forty thousand characters of prose over four hundred
// tool calls, so a brief is a summary by necessity. This is the size at which
// it stays one.
const BriefLimit = 6000

// Per-message caps. The last thing the agent said is the state of play and gets
// more room than the rest of them; a single message is never worth a page.
const (
	briefUser   = 1200
	briefAgent  = 800
	briefLast   = 2000
	briefCalls  = 6 // tool lines per message, mirroring the transcript's own fold
	briefMargin = 600
)

// Brief renders messages as a catch-up for an engine that was not there when
// they happened.
//
// Tool calls become the one line the transcript shows them as — Summary and
// Outcome already speak every engine's spelling — never their input JSON, which
// is eleven times the size of the conversation around it and is not what the
// next engine needs to know.
//
// What survives when it does not all fit is the end. What the next engine has
// to do is continue, and what it needs for that is what was just being done;
// the beginning is the part a reader can scroll back to and an agent can go and
// read again.
func Brief(msgs []Message, limit int) string {
	if len(msgs) == 0 {
		return ""
	}
	if limit <= 0 {
		limit = BriefLimit
	}

	blocks := make([]string, len(msgs))
	for i := range msgs {
		cap := briefAgent
		switch {
		case i == len(msgs)-1:
			cap = briefLast
		case msgs[i].Role == RoleUser:
			cap = briefUser
		}
		blocks[i] = briefBlock(&msgs[i], cap)
	}

	// Walk back from the newest until the budget is spent, then emit forward.
	room := limit - briefMargin
	first, used := len(blocks), 0
	for i := len(blocks) - 1; i >= 0; i-- {
		if blocks[i] == "" {
			first = i
			continue
		}
		if used+len(blocks[i])+1 > room && first < len(blocks) {
			break
		}
		used += len(blocks[i]) + 1
		first = i
	}

	var b strings.Builder
	b.WriteString(briefOpen)
	if first > 0 {
		// Silently lossy is the one thing a briefing may not be.
		b.WriteString("… " + plural(first, "earlier message") + " left out\n")
	}
	for _, blk := range blocks[first:] {
		if blk != "" {
			b.WriteString(blk)
			b.WriteByte('\n')
		}
	}
	// Computed over every message, including the ones that were cut: which
	// files have been touched is the most load-bearing fact the next engine can
	// be given, and it costs a line.
	if files := briefFiles(msgs); files != "" {
		b.WriteString("\nfiles touched: " + files + "\n")
	}
	b.WriteString(briefClose)
	return b.String()
}

// The wrapper. Tags because every model here is trained on them and because a
// closing tag survives newlines, shell quoting and a user message that is
// itself full of prose. The last sentence is what stops the end of the briefing
// being read as the instruction.
const briefOpen = "<handoff>\n" +
	"Another coding agent was working with the user in this project. Everything\n" +
	"between these tags already happened: it is a record, not a request. Do not\n" +
	"redo it. The user's new message begins after </handoff>.\n\n"

const briefClose = "</handoff>"

// briefBlock renders one message the way the transcript shows it.
func briefBlock(m *Message, cap int) string {
	var b strings.Builder

	if m.Shell != nil {
		// A command the user ran is part of the conversation rather than a
		// detour from it, and reading as one costs a line.
		b.WriteString("$ " + firstLine(m.Shell.Command))
		if m.Shell.Done {
			b.WriteString("  (exit " + strconv.Itoa(m.Shell.Exit) + ")")
		}
		return b.String()
	}

	who := "agent"
	if m.Role == RoleUser {
		who = "you"
	}
	if text := strings.TrimSpace(m.Text); text != "" {
		b.WriteString(who + ": " + elide(text, cap))
	}
	for i := range m.Tools {
		if i == briefCalls {
			b.WriteString("\n  · (+" + plural(len(m.Tools)-briefCalls, "more call") + ")")
			break
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("  · " + briefCall(&m.Tools[i]))
	}
	return b.String()
}

// briefCall is one tool call on one line: what it was asked to do and what came
// back, never the JSON of either.
func briefCall(t *ToolCall) string {
	line := t.Name
	if sum := t.Summary(); sum != "" {
		line += "  " + sum
	}
	switch {
	case t.Denied:
		return line + "  → denied"
	case t.IsError:
		return line + "  → failed"
	}
	if out := t.Outcome(); out != "" {
		return line + "  → " + out
	}
	return line
}

// briefFiles lists what the conversation wrote to, in the order it first
// touched them.
func briefFiles(msgs []Message) string {
	seen := map[string]bool{}
	var out []string
	for i := range msgs {
		for j := range msgs[i].Tools {
			t := &msgs[i].Tools[j]
			if !writesFiles(t.Name) {
				continue
			}
			p := t.Summary()
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
			if len(out) == 15 {
				return strings.Join(out, ", ") + ", …"
			}
		}
	}
	return strings.Join(out, ", ")
}

// writesFiles knows the write and edit tools by every spelling the engines use
// for them. It is the same vocabulary ToolCall.Summary matches on, for the same
// reason: a call means what it does, not what its engine calls it.
func writesFiles(name string) bool {
	switch strings.ToLower(name) {
	case "write_file", "write", "edit_file", "edit", "multiedit", "notebookedit":
		return true
	}
	return false
}

// elide cuts the middle out of an overlong message. The middle, because the
// first line says what it is about and the last says where it got to.
func elide(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	head := max * 2 / 3
	tail := max - head - 5
	if tail < 0 {
		return s[:max]
	}
	return strings.TrimSpace(s[:head]) + " … " + strings.TrimSpace(s[len(s)-tail:])
}
