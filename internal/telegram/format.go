package telegram

import (
	"html"
	"regexp"
	"strings"
)

// An agent answers in Markdown; Telegram renders a small HTML: b, i, s, u,
// code, pre, a, blockquote. Anything else must be escaped, and a message
// holds 4096 characters.

// chunkLimit leaves room under Telegram's 4096 for the tags the conversion
// adds.
const chunkLimit = 3500

var (
	reInlineCode = regexp.MustCompile("`([^`\n]+)`")
	reBold       = regexp.MustCompile(`\*\*([^*\n]+)\*\*|__([^_\n]+)__`)
	reItalic     = regexp.MustCompile(`(^|[\s(])\*([^*\s][^*\n]*?)\*`)
	reStrike     = regexp.MustCompile(`~~([^~\n]+)~~`)
	reLink       = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^)\s]+)\)`)
	reHeading    = regexp.MustCompile(`^#{1,6}\s+(.*)$`)
	reBullet     = regexp.MustCompile(`^(\s*)[-*+]\s+`)
	reFence      = regexp.MustCompile("^\\s*```\\s*([\\w+#.-]*)\\s*$")
)

// block is a run of Markdown: prose, or a fenced code block.
type block struct {
	code bool
	lang string
	text string
}

func blocks(md string) []block {
	var out []block
	var cur []string
	code, lang := false, ""
	flush := func() {
		if len(cur) > 0 {
			out = append(out, block{code: code, lang: lang, text: strings.Join(cur, "\n")})
		}
		cur = nil
	}
	for _, line := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		if m := reFence.FindStringSubmatch(line); m != nil {
			flush()
			if code {
				code, lang = false, ""
			} else {
				code, lang = true, m[1]
			}
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return out
}

// HTML converts Markdown to Telegram's HTML.
func HTML(md string) string {
	var b strings.Builder
	for i, bl := range blocks(md) {
		if i > 0 {
			b.WriteString("\n")
		}
		if bl.code {
			if bl.lang != "" {
				b.WriteString(`<pre><code class="language-` + html.EscapeString(bl.lang) + `">`)
			} else {
				b.WriteString("<pre><code>")
			}
			b.WriteString(html.EscapeString(bl.text))
			b.WriteString("</code></pre>")
			continue
		}
		b.WriteString(prose(bl.text))
	}
	return strings.TrimSpace(b.String())
}

func prose(text string) string {
	lines := strings.Split(text, "\n")
	var out []string
	var table []string
	flushTable := func() {
		if len(table) > 0 {
			out = append(out, "<pre>"+html.EscapeString(strings.Join(table, "\n"))+"</pre>")
			table = nil
		}
	}
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "|") {
			table = append(table, t)
			continue
		}
		flushTable()
		if m := reHeading.FindStringSubmatch(t); m != nil {
			out = append(out, "<b>"+inline(m[1])+"</b>")
			continue
		}
		if strings.HasPrefix(t, ">") {
			out = append(out, "<blockquote>"+inline(strings.TrimSpace(strings.TrimPrefix(t, ">")))+"</blockquote>")
			continue
		}
		if t == "---" || t == "***" {
			out = append(out, "──────")
			continue
		}
		line = reBullet.ReplaceAllString(line, "$1• ")
		out = append(out, inline(line))
	}
	flushTable()
	return strings.Join(out, "\n")
}

// inline converts one line's spans. Code spans are cut out first, so that
// nothing inside them is read as Markdown.
func inline(s string) string {
	var codes []string
	s = reInlineCode.ReplaceAllStringFunc(s, func(m string) string {
		codes = append(codes, m[1:len(m)-1])
		return "\x00" + string(rune('0'+len(codes)-1)) + "\x00"
	})
	s = html.EscapeString(s)
	s = reLink.ReplaceAllString(s, `<a href="$2">$1</a>`)
	s = reBold.ReplaceAllStringFunc(s, func(m string) string {
		return "<b>" + strings.Trim(m, "*_") + "</b>"
	})
	s = reItalic.ReplaceAllString(s, "$1<i>$2</i>")
	s = reStrike.ReplaceAllString(s, "<s>$1</s>")
	for i, c := range codes {
		s = strings.Replace(s, "\x00"+string(rune('0'+i))+"\x00", "<code>"+html.EscapeString(c)+"</code>", 1)
	}
	return s
}

// Chunks splits Markdown into pieces that each convert to a message: at
// blank lines where it can, at line ends where it must, and a code block cut
// in two is closed and reopened, so each piece renders on its own.
func Chunks(md string) []string {
	var out []string
	var cur strings.Builder
	push := func() {
		if strings.TrimSpace(cur.String()) != "" {
			out = append(out, strings.TrimSpace(cur.String()))
		}
		cur.Reset()
	}
	add := func(s string) {
		if cur.Len()+len(s)+1 > chunkLimit {
			push()
		}
		if cur.Len() > 0 {
			cur.WriteString("\n")
		}
		cur.WriteString(s)
	}
	for _, bl := range blocks(md) {
		if bl.code {
			fence := "```" + bl.lang
			piece := []string{}
			size := len(fence) + 4
			for _, line := range strings.Split(bl.text, "\n") {
				for len(line) > chunkLimit-len(fence)-8 {
					// One line longer than a message: cut it.
					cut := chunkLimit - len(fence) - 8
					piece = append(piece, line[:cut])
					add(fence + "\n" + strings.Join(piece, "\n") + "\n```")
					push()
					piece, size, line = nil, len(fence)+4, line[cut:]
				}
				if size+len(line)+1 > chunkLimit-8 && len(piece) > 0 {
					add(fence + "\n" + strings.Join(piece, "\n") + "\n```")
					push()
					piece, size = nil, len(fence)+4
				}
				piece = append(piece, line)
				size += len(line) + 1
			}
			add(fence + "\n" + strings.Join(piece, "\n") + "\n```")
			continue
		}
		for _, para := range strings.Split(bl.text, "\n\n") {
			if len(para) <= chunkLimit {
				add(para + "\n")
				continue
			}
			for _, line := range strings.Split(para, "\n") {
				for len(line) > chunkLimit {
					add(line[:chunkLimit])
					line = line[chunkLimit:]
				}
				add(line)
			}
		}
	}
	push()
	return out
}

var reTag = regexp.MustCompile(`<[^>]+>`)

// plain is HTML's text, for when Telegram will not take the HTML.
func plain(h string) string { return html.UnescapeString(reTag.ReplaceAllString(h, "")) }

// esc escapes text for HTML.
func esc(s string) string { return html.EscapeString(s) }
