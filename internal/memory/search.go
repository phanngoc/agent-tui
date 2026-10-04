package memory

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// Tokens splits text into lowercase words: runs of letters, digits and
// underscores, which is the tokenizer TencentDB uses for everything but
// Chinese. Vietnamese and Japanese keep their marks, so "lưu" and "luu" stay
// different words — the same as a person would read them.
func Tokens(s string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, stem(strings.ToLower(string(cur))))
			cur = cur[:0]
		}
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || unicode.Is(unicode.Mn, r) {
			// CJK has no spaces; each ideograph is a word of its own, which
			// is crude but finds the right records far more often than
			// treating a whole sentence as one token.
			if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) {
				flush()
				out = append(out, string(r))
				continue
			}
			cur = append(cur, r)
			continue
		}
		flush()
	}
	flush()
	return out
}

// stem folds the commonest English inflections, so "deploys", "deployed"
// and "deploying" find "deploy". Only plain ASCII words long enough to
// survive it are touched: a Vietnamese word's final letters are not
// suffixes.
func stem(w string) string {
	if len(w) < 5 {
		return w
	}
	for i := 0; i < len(w); i++ {
		if c := w[i]; c < 'a' || c > 'z' {
			return w
		}
	}
	if strings.HasSuffix(w, "ss") || strings.HasSuffix(w, "us") || strings.HasSuffix(w, "is") {
		return w // process, status, analysis
	}
	for _, suf := range []string{"ing", "ies", "ed", "es", "s"} {
		if strings.HasSuffix(w, suf) && len(w)-len(suf) >= 4 {
			w = strings.TrimSuffix(w, suf)
			if suf == "ies" {
				w += "y"
			}
			return w
		}
	}
	return w
}

// stop words that carry no topic, in the languages this app is used in.
var stop = map[string]bool{
	"the": true, "a": true, "an": true, "and": true, "or": true, "of": true, "to": true,
	"in": true, "is": true, "it": true, "for": true, "on": true, "with": true, "this": true,
	"that": true, "be": true, "are": true, "was": true, "i": true, "you": true, "me": true,
	"và": true, "là": true, "của": true, "có": true, "cho": true, "với": true, "này": true,
	"thì": true, "các": true, "những": true, "một": true, "được": true, "không": true,
}

// Hit is a record and how well it matched.
type Hit struct {
	Record Record  `json:"record"`
	Score  float64 `json:"score"`
}

// Search ranks records against a query with BM25 and normalises the score to
// 0..1 as rel/(1+rel). Records with no term in common are left out.
func Search(records []Record, query string, limit int) []Hit {
	q := uniq(Tokens(query))
	if len(q) == 0 || len(records) == 0 {
		return nil
	}
	const k1, b = 1.2, 0.75
	docs := make([][]string, len(records))
	df := map[string]int{}
	total := 0
	for i, r := range records {
		docs[i] = Tokens(r.Content + " " + r.Scene)
		total += len(docs[i])
		for _, t := range uniq(docs[i]) {
			df[t]++
		}
	}
	avg := float64(total) / float64(len(records))
	n := float64(len(records))

	var hits []Hit
	for i, d := range docs {
		tf := map[string]int{}
		for _, t := range d {
			tf[t]++
		}
		var score float64
		for _, t := range q {
			f := float64(tf[t])
			if f == 0 {
				continue
			}
			idf := math.Log(1 + (n-float64(df[t])+0.5)/(float64(df[t])+0.5))
			score += idf * f * (k1 + 1) / (f + k1*(1-b+b*float64(len(d))/avg))
		}
		if score <= 0 {
			continue
		}
		norm := score / (1 + score)
		// Priority breaks ties and nudges: of two records that match as
		// well, the one that matters more comes first.
		if p := records[i].Priority; p > 0 {
			norm *= 1 + float64(p)/400
		}
		hits = append(hits, Hit{Record: records[i], Score: norm})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// Recall picks what goes into a prompt: the best matches, at most limit. A
// small corpus returns every match, because BM25 scores are unreliable when
// there are few documents to compare; a larger one keeps those at or above
// threshold.
func Recall(records []Record, query string, limit int, threshold float64) []Hit {
	all := Search(records, query, 0)
	if len(all) <= limit {
		return all
	}
	var out []Hit
	for _, h := range all {
		if h.Score >= threshold {
			out = append(out, h)
		}
		if len(out) == limit {
			break
		}
	}
	return out
}

func uniq(ts []string) []string {
	seen := map[string]bool{}
	out := ts[:0:0]
	for _, t := range ts {
		if seen[t] || stop[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}
