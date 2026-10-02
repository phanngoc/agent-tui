package clipboard

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestCFHTMLOffsetsFrameTheFragment: Windows readers find the fragment by the
// byte offsets in the header, so they must be byte offsets — a fragment in
// Vietnamese is longer in bytes than in characters.
func TestCFHTMLOffsetsFrameTheFragment(t *testing.T) {
	frag := "<p><b>Chặn vĩnh viễn</b> 目視審査</p>"
	doc := string(cfHTML(frag))

	at := func(key string) int {
		m := regexp.MustCompile(key + `:(\d{10})`).FindStringSubmatch(doc)
		if m == nil {
			t.Fatalf("no %s in %q", key, doc)
		}
		n, _ := strconv.Atoi(m[1])
		return n
	}
	if got := doc[at("StartFragment"):at("EndFragment")]; got != frag {
		t.Errorf("fragment by offsets = %q", got)
	}
	if !strings.HasPrefix(doc[at("StartHTML"):], "<html>") || at("EndHTML") != len(doc) {
		t.Errorf("document offsets are wrong:\n%s", doc)
	}
}
