package wiki

import (
	"regexp"
	"strconv"
	"strings"
)

// Page types, and the folder each is kept in. A source page summarises one
// raw document; entities are concrete things (a service, a table, a team),
// concepts abstract ones (a rule, a method); comparisons and syntheses are
// written across several pages, by an ingest or by the agent answering a
// question.
const (
	TypeSource     = "source"
	TypeEntity     = "entity"
	TypeConcept    = "concept"
	TypeComparison = "comparison"
	TypeSynthesis  = "synthesis"
)

// Types lists the page types.
var Types = []string{TypeSource, TypeEntity, TypeConcept, TypeComparison, TypeSynthesis}

// dirFor is the folder a type's pages go in. A model sometimes invents a type;
// the near ones are folded in and the rest kept apart in other/.
func dirFor(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case TypeSource:
		return "sources"
	case TypeEntity:
		return "entities"
	case TypeConcept, "methodology", "method":
		return "concepts"
	case TypeComparison:
		return "comparisons"
	case TypeSynthesis, "thesis", "finding", "query", "answer":
		return "synthesis"
	}
	return "other"
}

// typeOfDir is the type a page in this folder has when its header says none.
func typeOfDir(dir string) string {
	for _, t := range Types {
		if dirFor(t) == dir {
			return t
		}
	}
	return "other"
}

// Page is one wiki page: a Markdown file with a small YAML header.
type Page struct {
	// ID is the path under pages/ without .md, e.g. "entities/redis".
	ID          string
	Type        string
	Title       string
	Description string
	// Sources are the raw files the page was written from, "agent" for
	// what the agent filed while answering, "user" for an edit by hand.
	Sources []string
	Tags    []string
	Updated string
	// Locked pages were edited by hand: an ingest does not merge into them.
	Locked bool
	// Extra keeps header fields this package does not know, in order.
	Extra [][2]string
	Body  string
}

var slugRE = regexp.MustCompile(`[^\p{L}\p{N}\p{M}]+`)

// Slugify turns a title into a file name. Letters of every script survive,
// marks included, so "Đăng nhập" and "Dang nhap" stay two different pages —
// as two different titles should.
func Slugify(s string) string {
	s = strings.Trim(slugRE.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-"), "-")
	if r := []rune(s); len(r) > 80 {
		s = strings.TrimRight(string(r[:80]), "-")
	}
	if s == "" {
		s = "page"
	}
	return s
}

// CanonicalID is where a page belongs: its type's folder and its title's
// slug. The path a model chose is never used, so the same title always lands
// on the same file — the whole of the dedup an ingest can rely on.
func CanonicalID(typ, title string) string { return dirFor(typ) + "/" + Slugify(title) }

// ParsePage reads a page file. A file without a header is all body.
func ParsePage(id, text string) Page {
	p := Page{ID: id}
	text = strings.TrimPrefix(strings.ReplaceAll(text, "\r\n", "\n"), "\xef\xbb\xbf") // a BOM
	head, body, ok := splitFrontmatter(text)
	if !ok {
		p.Body = strings.TrimSpace(text)
		return p
	}
	p.Body = strings.TrimSpace(body)
	var listKey string
	for _, line := range strings.Split(head, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "- ") && listKey != "" {
			p.addList(listKey, unquote(strings.TrimSpace(t[2:])))
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		listKey = ""
		switch k {
		case "type":
			p.Type = unquote(v)
		case "title":
			p.Title = unquote(v)
		case "description":
			p.Description = unquote(v)
		case "updated", "timestamp":
			p.Updated = unquote(v)
		case "locked":
			p.Locked = unquote(v) == "true"
		case "sources", "tags":
			if v == "" {
				listKey = k
				continue
			}
			for _, item := range inlineList(v) {
				p.addList(k, item)
			}
		default:
			p.Extra = append(p.Extra, [2]string{k, v})
		}
	}
	return p
}

func (p *Page) addList(k, v string) {
	if v == "" {
		return
	}
	if k == "sources" {
		p.Sources = union(p.Sources, []string{v})
	} else {
		p.Tags = union(p.Tags, []string{v})
	}
}

func splitFrontmatter(text string) (head, body string, ok bool) {
	if !strings.HasPrefix(text, "---\n") {
		return "", text, false
	}
	rest := text[4:]
	i := strings.Index(rest, "\n---")
	if i < 0 {
		return "", text, false
	}
	head = rest[:i]
	body = rest[i+4:]
	if j := strings.IndexByte(body, '\n'); j >= 0 {
		body = body[j+1:]
	} else {
		body = ""
	}
	return head, body, true
}

func inlineList(v string) []string {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "[") {
		return []string{unquote(v)}
	}
	v = strings.TrimSuffix(strings.TrimPrefix(v, "["), "]")
	var out []string
	add := func(item string) {
		if item = unquote(strings.TrimSpace(item)); item != "" {
			out = append(out, item)
		}
	}
	// Split at commas outside quotes.
	var q rune
	start := 0
	for i, r := range v {
		switch {
		case q != 0 && r == q:
			q = 0
		case q == 0 && (r == '"' || r == '\''):
			q = r
		case q == 0 && r == ',':
			add(v[start:i])
			start = i + 1
		}
	}
	add(v[start:])
	return out
}

func unquote(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		if s, err := strconv.Unquote(v); err == nil {
			return s
		}
		return v[1 : len(v)-1]
	}
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return strings.ReplaceAll(v[1:len(v)-1], "''", "'")
	}
	return v
}

// quote writes a scalar YAML reads back as the same string.
func quote(v string) string {
	if v == "" {
		return `""`
	}
	if strings.ContainsAny(v, ":#[]{},&*!|>'\"%@`\n") || v != strings.TrimSpace(v) ||
		v == "true" || v == "false" || v == "null" || strings.HasPrefix(v, "- ") {
		return strconv.Quote(v)
	}
	return v
}

// String renders the page as its file.
func (p Page) String() string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("type: " + quote(p.Type) + "\n")
	b.WriteString("title: " + quote(p.Title) + "\n")
	if p.Description != "" {
		b.WriteString("description: " + quote(p.Description) + "\n")
	}
	for _, l := range []struct {
		k  string
		vs []string
	}{{"sources", p.Sources}, {"tags", p.Tags}} {
		if len(l.vs) == 0 {
			continue
		}
		b.WriteString(l.k + ":\n")
		for _, v := range l.vs {
			b.WriteString("  - " + quote(v) + "\n")
		}
	}
	if p.Updated != "" {
		b.WriteString("updated: " + quote(p.Updated) + "\n")
	}
	if p.Locked {
		b.WriteString("locked: true\n")
	}
	for _, kv := range p.Extra {
		b.WriteString(kv[0] + ": " + kv[1] + "\n")
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(p.Body))
	b.WriteString("\n")
	return b.String()
}

// Snippet is what a listing shows of a page: its description, or failing
// that the start of its body.
func (p Page) Snippet() string {
	if p.Description != "" {
		return p.Description
	}
	s := strings.Join(strings.Fields(p.Body), " ")
	if r := []rune(s); len(r) > 120 {
		s = string(r[:120]) + "…"
	}
	return s
}

var linkRE = regexp.MustCompile(`\[\[([^\[\]|\n]+)(?:\|([^\[\]\n]*))?\]\]`)

// Links lists the targets of a page's [[wikilinks]], as written.
func (p Page) Links() []string {
	var out []string
	for _, m := range linkRE.FindAllStringSubmatch(p.Body, -1) {
		out = union(out, []string{strings.TrimSpace(m[1])})
	}
	return out
}

// union appends what b has that a lacks, keeping a's order.
func union(a, b []string) []string {
	seen := make(map[string]bool, len(a))
	for _, v := range a {
		seen[v] = true
	}
	for _, v := range b {
		if v != "" && !seen[v] {
			seen[v] = true
			a = append(a, v)
		}
	}
	return a
}
