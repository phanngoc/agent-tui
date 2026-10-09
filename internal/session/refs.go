package session

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Board columns, in order. The empty column is the backlog.
const (
	BoardBacklog = "backlog"
	BoardTodo    = "todo"
	BoardDoing   = "doing"
	BoardDone    = "done"
)

// BoardColumns lists the columns, left to right.
var BoardColumns = []string{BoardBacklog, BoardTodo, BoardDoing, BoardDone}

// ValidBoard reports whether c is a column; "" is, meaning the backlog.
func ValidBoard(c string) bool {
	if c == "" {
		return true
	}
	for _, v := range BoardColumns {
		if v == c {
			return true
		}
	}
	return false
}

// Ref is a link pinned to a conversation.
type Ref struct {
	URL   string    `json:"url"`
	Title string    `json:"title,omitempty"`
	Added time.Time `json:"added"`
}

// Link is a link said in a conversation: where, by whom, and around what.
type Link struct {
	URL   string `json:"url"`
	Kind  string `json:"kind"`  // slack, github, google, backlog, jira, confluence, miro, notion, web
	Label string `json:"label"` // short, for a chip: "owner/repo#12", "Slack C0A8WMPPTEF"
	// At is the index of the first message it is in; Role who said it.
	At      int       `json:"at"`
	Role    string    `json:"role"`
	When    time.Time `json:"when"`
	Snippet string    `json:"snippet"` // the words around it
	// Times counts the messages it is in.
	Times int `json:"times"`
}

var urlRE = regexp.MustCompile(`https?://[^\s<>"'` + "`" + `\]\[(){}|\\^]+`)

// cleanURL trims what a sentence leaves on the end of a link.
func cleanURL(u string) string {
	return strings.TrimRight(u, ".,;:!?…'\"”’>*_")
}

// Links finds the links said in a conversation — by the user, and in the
// agent's prose — first said first. Tool output is left out: it is file
// dumps and fetched pages, whose links nobody chose.
func (s *Session) Links() []Link {
	var out []Link
	idx := map[string]int{}
	for i, m := range s.Messages {
		if m.Role != RoleUser && m.Role != RoleAssistant {
			continue
		}
		seenHere := map[string]bool{}
		for _, loc := range urlRE.FindAllStringIndex(m.Text, -1) {
			u := cleanURL(m.Text[loc[0]:loc[1]])
			p, err := url.Parse(u)
			if err != nil || p.Host == "" {
				continue
			}
			if j, ok := idx[u]; ok {
				if !seenHere[u] {
					out[j].Times++
				}
				seenHere[u] = true
				continue
			}
			seenHere[u] = true
			kind, label := Classify(u)
			idx[u] = len(out)
			out = append(out, Link{URL: u, Kind: kind, Label: label, At: i, Role: string(m.Role), When: m.At,
				Snippet: around(m.Text, loc[0], loc[0]+len(u)), Times: 1})
		}
	}
	return out
}

// Origin is where the conversation came from: the first pinned link, or
// failing that the first link the user gave.
func (s *Session) Origin() (Link, bool) {
	if len(s.Refs) > 0 {
		kind, label := Classify(s.Refs[0].URL)
		if s.Refs[0].Title != "" {
			label = s.Refs[0].Title
		}
		return Link{URL: s.Refs[0].URL, Kind: kind, Label: label, At: -1}, true
	}
	for _, l := range s.Links() {
		if l.Role == string(RoleUser) {
			return l, true
		}
	}
	return Link{}, false
}

// around is the text either side of a link, on one line.
func around(text string, from, to int) string {
	const span = 70
	a, b := max(0, from-span), min(len(text), to+span)
	for a > 0 && a < len(text) && !isRuneStart(text[a]) {
		a--
	}
	for b < len(text) && !isRuneStart(text[b]) {
		b++
	}
	s := strings.Join(strings.Fields(text[a:b]), " ")
	if a > 0 {
		s = "…" + s
	}
	if b < len(text) {
		s += "…"
	}
	return s
}

func isRuneStart(c byte) bool { return c&0xC0 != 0x80 }

// Classify names what a link points at, and labels it short enough for a
// chip.
func Classify(raw string) (kind, label string) {
	p, err := url.Parse(raw)
	if err != nil {
		return "web", raw
	}
	host := strings.TrimPrefix(strings.ToLower(p.Host), "www.")
	parts := strings.FieldsFunc(p.Path, func(r rune) bool { return r == '/' })
	at := func(i int) string {
		if i < len(parts) {
			return parts[i]
		}
		return ""
	}
	switch {
	case strings.HasSuffix(host, "slack.com"):
		// /archives/<channel>/p<ts>: the ts is when the message was sent, in
		// microseconds, which tells one thread of a channel from the next
		// where the channel's id would not.
		if at(0) == "archives" && at(1) != "" {
			if ts := strings.TrimPrefix(at(2), "p"); len(ts) > 10 && ts != at(2) {
				if sec, err := strconv.ParseInt(ts[:10], 10, 64); err == nil {
					return "slack", "Slack thread · " + time.Unix(sec, 0).Local().Format("2006-01-02 15:04")
				}
			}
			return "slack", "Slack " + at(1)
		}
		return "slack", "Slack"
	case host == "github.com":
		switch {
		case len(parts) >= 4 && (at(2) == "pull" || at(2) == "issues"):
			return "github", at(0) + "/" + at(1) + "#" + at(3)
		case len(parts) >= 2:
			return "github", at(0) + "/" + at(1)
		}
		return "github", "GitHub"
	case host == "docs.google.com":
		switch at(0) {
		case "spreadsheets":
			return "google", "Google Sheet"
		case "presentation":
			return "google", "Google Slides"
		}
		return "google", "Google Doc"
	case host == "drive.google.com":
		return "google", "Google Drive"
	case strings.Contains(host, "backlog.com") || strings.Contains(host, "backlog.jp"):
		// /view/PROJ-123
		if at(0) == "view" && at(1) != "" {
			return "backlog", at(1)
		}
		return "backlog", "Backlog"
	case strings.HasSuffix(host, "atlassian.net") && at(0) == "browse":
		return "jira", at(1)
	case strings.HasSuffix(host, "atlassian.net") && at(0) == "wiki":
		return "confluence", "Confluence"
	case strings.HasSuffix(host, "miro.com"):
		return "miro", "Miro board"
	case strings.HasSuffix(host, "notion.so") || strings.HasSuffix(host, "notion.site"):
		return "notion", "Notion"
	}
	label = host
	if len(parts) > 0 {
		last := parts[len(parts)-1]
		if r := []rune(last); len(r) > 32 {
			last = string(r[:32]) + "…"
		}
		label += "/…/" + last
		if len(parts) == 1 {
			label = host + "/" + last
		}
	}
	return "web", label
}
