package session

import (
	"strconv"
	"strings"
)

// What a tool call looks like on one line, before and after it runs.
//
// Summary says what was asked for and lives next to the ToolCall it renders.
// Outcome says what came back — how many lines were read, what a command
// exited with — because a transcript that only ever says "✓ read_file" tells
// you the agent did something, not what it found.

// Outcome is a few words about the result, for the line the call is shown on.
//
// It reads the result rather than being told: a tool's own text already leads
// with what it did, and every engine here produces one, including the CLIs
// whose tools this process never runs. The formats the built-in tools use are
// pinned by a test next to them, so a reworded result fails there rather than
// quietly going blank here.
func (t ToolCall) Outcome() string {
	if !t.Done || t.Denied || t.Result == "" {
		return ""
	}
	body := strings.TrimRight(t.Result, "\n")
	if body == "" {
		return ""
	}
	lines := strings.Split(body, "\n")

	switch strings.ToLower(t.Name) {
	case "bash", "shell", "run":
		// The output is the command's own, so it has no summary line of its
		// own to borrow. What is worth knowing is that it ended and how much
		// it said.
		if t.IsError {
			return "failed"
		}
		if len(lines) == 1 && strings.HasPrefix(lines[0], "(no output") {
			return "no output"
		}
		return plural(len(lines), "line")

	case "list_dir", "ls":
		// The first line is the directory itself, which the summary already
		// named; the rest are what is in it.
		return plural(max(0, len(lines)-1), "entry")
	}

	// Everything else leads with a sentence about itself: "created x (12
	// bytes, 3 lines)", "8 match(es) in 3 file(s):", "x.go (lines 1-40 of
	// 249)". The path in it is already on the line, so it comes off.
	head := strings.TrimRight(strings.TrimSpace(lines[0]), ":")
	words := strings.Fields(head)

	// The path is already on the line, so the result's own copy of it comes
	// off — but only where these results put it, as a word of its own at the
	// front or just after the verb. A search says `1 match(es) for "a.go"`,
	// where the same text is the answer rather than a repetition of the
	// question, and taking it out would leave the line saying nothing.
	if sum := t.Summary(); sum != "" {
		for i := 0; i < len(words) && i < 2; i++ {
			if words[i] == sum {
				words = append(words[:i], words[i+1:]...)
				break
			}
		}
	}
	head = strings.Join(words, " ")
	// Taking the path out can leave the rest inside the brackets that held it
	// apart: "(lines 1-40 of 249)". They only come off when they wrap the whole
	// of what is left — "8 match(es) in 3 file(s)" ends with one of its own.
	if strings.HasPrefix(head, "(") && strings.HasSuffix(head, ")") {
		head = head[1 : len(head)-1]
	}
	if head == "" || len(head) > 48 {
		return ""
	}
	return head
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	if strings.HasSuffix(word, "y") {
		return strconv.Itoa(n) + " " + word[:len(word)-1] + "ies"
	}
	return strconv.Itoa(n) + " " + word + "s"
}

// partialFields pulls the string fields out of JSON that has not finished
// arriving.
//
// A tool call is streamed as a run of fragments, so for most of its life it is
// not JSON at all — `{"path": "internal/ui/ch` — and decoding it strictly
// yields nothing at all. This reads what is there and is not troubled by the
// fact that it does not end. Only strings are collected: a number or a nested
// object is never what the line shows.
func partialFields(raw []byte) map[string]any {
	out := make(map[string]any, 4)
	s := string(raw)

	i := skipSpace(s, 0)
	if i >= len(s) || s[i] != '{' {
		return out
	}
	for i++; ; {
		if i = skipSpace(s, i); i >= len(s) || s[i] != '"' {
			return out
		}
		key, next := scanString(s, i)
		if i = skipSpace(s, next); i >= len(s) || s[i] != ':' {
			return out
		}
		if i = skipSpace(s, i+1); i >= len(s) {
			return out
		}
		if s[i] == '"' {
			val, next := scanString(s, i)
			out[key] = val
			i = next
		} else {
			next, ok := skipValue(s, i)
			if !ok {
				return out
			}
			i = next
		}
		if i = skipSpace(s, i); i >= len(s) || s[i] != ',' {
			return out
		}
		i++
	}
}

// scanString reads a JSON string starting at the opening quote and returns it
// with the index just past its close — or past the end, for one still being
// written.
func scanString(s string, i int) (string, int) {
	var b strings.Builder
	for i++; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if i+1 >= len(s) {
				return b.String(), i + 1
			}
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'u':
				// A \uXXXX escape mid-stream may be half-written. Nothing that
				// reaches a summary line needs it decoded.
				i = min(i+4, len(s)-1)
			default:
				b.WriteByte(s[i])
			}
		case '"':
			return b.String(), i + 1
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String(), len(s)
}

// skipValue steps over a non-string value, however unfinished it is.
func skipValue(s string, i int) (int, bool) {
	depth := 0
	for ; i < len(s); i++ {
		switch s[i] {
		case '{', '[':
			depth++
		case '}', ']':
			if depth--; depth <= 0 {
				return i + 1, depth == 0
			}
		case '"':
			_, next := scanString(s, i)
			i = next - 1
		case ',':
			if depth == 0 {
				return i, true
			}
		}
	}
	return i, false
}

func skipSpace(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	return i
}
