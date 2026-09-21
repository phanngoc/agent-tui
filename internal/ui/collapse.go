package ui

import (
	"strconv"

	"github.com/phanngoc/agent-tui/internal/session"
)

// A turn keeps its last few calls and folds the rest away.
//
// A turn that reads twenty files leaves twenty lines saying it read them, and
// by the time you are looking at the answer those lines have already done
// their work: you watched them go past while the turn was running, one at a
// time, which is what they are for. What is left afterwards is a wall between
// the question and the answer to it.
//
// So the recent ones stay — they are the ones the answer is about to refer to
// — and the rest become a line saying how many there were. Nothing is thrown
// away: alt+o brings them all back.

// callsShown is how many calls survive the fold. Enough to see what the agent
// was doing just before it spoke; few enough that the answer is still on the
// same screen as the question.
const callsShown = 5

// callPlan is what to do with one message's tool calls.
type callPlan struct {
	// head is false for a message that continues the block above it.
	head bool
	// skip is how many of this message's calls are folded away.
	skip int
	// note is the number to report on the folded-away line, which is drawn
	// once per turn, immediately before the first call that survived.
	note int
}

// planCalls decides, for every message, which of its calls to draw.
//
// It works over runs of consecutive agent steps rather than over messages,
// because a turn is a run: the calls that ought to fold together are spread
// across as many messages as the agent took round trips, and folding each
// message separately would keep the last five of each.
func planCalls(msgs []session.Message, keep int, all bool) []callPlan {
	out := make([]callPlan, len(msgs))
	for i := range out {
		out[i].head = i == 0 || !isStep(msgs[i]) || !isStep(msgs[i-1])
	}
	if all {
		return out
	}

	for i := 0; i < len(msgs); {
		if !isStep(msgs[i]) {
			i++
			continue
		}
		// The run is this message and every step after it.
		j, total := i, 0
		for ; j < len(msgs) && isStep(msgs[j]); j++ {
			total += len(msgs[j].Tools)
		}
		// Folding one call away costs a line to say so, which is no saving at
		// all, so a turn is only folded when there are at least two to hide.
		if drop := total - keep; drop > 1 {
			noted := false
			for k := i; k < j && drop > 0; k++ {
				n := min(drop, len(msgs[k].Tools))
				out[k].skip = n
				drop -= n
				// The line goes where the first surviving call is, which is
				// this message when it has any left, and the next one along
				// when it does not.
				if !noted && n < len(msgs[k].Tools) {
					out[k].note, noted = total-keep, true
				}
			}
			if !noted {
				for k := i; k < j; k++ {
					if len(msgs[k].Tools) > out[k].skip {
						out[k].note = total - keep
						break
					}
				}
			}
		}
		i = j
	}
	return out
}

// foldedLine is what stands in for the calls that were folded away. It says
// how many there were and how to get them back, because a count with no way
// to open it is just a number.
func (m *Model) foldedLine(n int) string {
	bar := m.st.AgentBar.Render("▎")
	return bar + "  " + m.st.Faint.Render(
		"… "+strconv.Itoa(n)+" earlier "+noun(n, "call")+"  ·  alt+o shows them")
}

func noun(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// toggleCalls folds the calls away or brings them back, everywhere at once.
// Folding is about reading a transcript, and a setting that applied to one
// turn would have to be set again at the next one.
func (m *Model) toggleCalls() {
	m.showAllCalls = !m.showAllCalls
	m.invalidateChat()
	if m.showAllCalls {
		m.notice = "showing every call"
		return
	}
	m.notice = "showing the last " + strconv.Itoa(callsShown) + " calls of each turn"
}
