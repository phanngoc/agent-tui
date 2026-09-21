package search

import (
	"regexp"
	"strings"
)

// A query compiled once, for scanning things that are not files.
//
// Run reads a file at a time, so compiling the query inside its worker costs
// nothing. Searching a conversation is the opposite shape: thousands of short
// messages, none of them a file, and compiling per input would be most of the
// work. So the query becomes a value, and Run is built on the same one — which
// is what keeps the smart-case rule and the literal/regex split from having two
// implementations that can drift apart.

// Matcher is a compiled query, reusable across many small inputs.
type Matcher struct {
	re     *regexp.Regexp
	pat    []byte
	lowPat []byte
	// fold is set when the query is matched case-insensitively, which is what
	// decides whether the haystack has to be lowered before scanning it.
	fold bool
	// buf is a scratch lowercase copy of the last input, kept so a scan over
	// many messages does not allocate one per message. A Matcher is therefore
	// not safe for concurrent use; Run gives each worker its own.
	buf []byte
}

// Compile prepares a query.
//
// Smart case is applied here rather than at each call site: a query with an
// uppercase letter in it is treated as case sensitive, the way ripgrep and
// Sublime both behave.
func Compile(o Options) (*Matcher, error) {
	o.normalise()
	if strings.TrimSpace(o.Query) == "" {
		return nil, nil
	}
	if o.Regex {
		expr := o.Query
		if !o.CaseSensitive {
			expr = "(?i)" + expr
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, err
		}
		return &Matcher{re: re}, nil
	}
	if !o.CaseSensitive && strings.ToLower(o.Query) != o.Query {
		o.CaseSensitive = true
	}
	pat := []byte(o.Query)
	return &Matcher{
		pat:    pat,
		lowPat: asciiLowerCopy(pat),
		fold:   !o.CaseSensitive,
	}, nil
}

// Scan returns the matches in text — at most one per line, grep-style — with
// Path set to label and Line counted from 1 within text.
func (m *Matcher) Scan(label string, text []byte) []Match {
	if m == nil || len(text) == 0 {
		return nil
	}
	if m.re != nil {
		return scanRegex(label, text, m.re)
	}
	if !m.fold {
		return scanLiteral(label, text, text, m.pat)
	}
	if cap(m.buf) < len(text) {
		m.buf = make([]byte, len(text))
	}
	m.buf = m.buf[:len(text)]
	asciiLowerInto(m.buf, text)
	return scanLiteral(label, text, m.buf, m.lowPat)
}

// clone gives a worker its own scratch buffer. The compiled query is shared;
// only the buffer is not.
func (m *Matcher) clone() *Matcher {
	if m == nil {
		return nil
	}
	c := *m
	c.buf = nil
	return &c
}
