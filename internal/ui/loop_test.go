package ui

import "testing"

func TestParseLoop(t *testing.T) {
	cases := []struct{ in, every, prompt string }{
		{"5m check the deploy", "5m", "check the deploy"},
		{"30s poll", "1m", "poll"},
		{"check the deploy every 2 hours", "2h", "check the deploy"},
		{"check whether CI passed", "", "check whether CI passed"},
		{"2h", "2h", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		every, prompt := parseLoop(c.in)
		if every != c.every || prompt != c.prompt {
			t.Errorf("parseLoop(%q) = %q, %q; want %q, %q", c.in, every, prompt, c.every, c.prompt)
		}
	}
}
