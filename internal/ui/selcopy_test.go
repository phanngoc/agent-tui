package ui

import (
	"strings"
	"testing"
)

const reportSrc = `## 4. Trạng thái implement

- **FE**: đã ship đầy đủ — 36 file / 24 màn đăng ký đi qua ` + "`useEntryPathClose`" + `.
- **Native** (iOS/Android): không render dialog này.
- **BE**: touchpoint duy nhất là bootstrap trả ` + "`partnerName`" + `.

## 5. Việc thật sự còn lại

Đang có drift chưa đóng giữa hai nguồn.`

// A selection of whole bullets, drawn wrapped and without their marks, traces
// back to the markdown lines it was drawn from — and only those.
func TestASelectionTracesToItsSourceLines(t *testing.T) {
	drawn := "• FE: đã ship đầy đủ — 36 file / 24 màn đăng ký đi\n  qua useEntryPathClose.\n" +
		"• Native (iOS/Android): không render dialog này.\n" +
		"• BE: touchpoint duy nhất là bootstrap trả\n  partnerName."
	got, ok := traceSource(drawn, reportSrc)
	if !ok {
		t.Fatal("the selection was not traced")
	}
	if !strings.HasPrefix(got, "- **FE**") || !strings.HasSuffix(got, "`partnerName`.") {
		t.Errorf("traced to:\n%s", got)
	}
	if strings.Contains(got, "Trạng thái") || strings.Contains(got, "drift") {
		t.Errorf("the trace took lines outside the selection:\n%s", got)
	}
}

// A selection that starts on the speaker's heading, which the source does not
// have, still finds where the prose begins.
func TestATraceWalksPastWhatTheSourceLacks(t *testing.T) {
	drawn := "agent  15:40\n4. TRẠNG THÁI IMPLEMENT\n\n• FE: đã ship đầy đủ"
	got, ok := traceSource(drawn, reportSrc)
	if !ok || !strings.HasPrefix(got, "## 4. Trạng thái") || !strings.HasSuffix(got, "`useEntryPathClose`.") {
		t.Errorf("got %q ok=%v", got, ok)
	}
}

// Half a line is copied as it was selected, not widened to the line.
func TestPartOfOneLineIsNotTraced(t *testing.T) {
	if got, ok := traceSource("touchpoint duy nhất", reportSrc); ok {
		t.Errorf("a phrase was widened to %q", got)
	}
}

// Tool output is not in the prose; it goes as drawn.
func TestAnUntraceableSelectionIsNotTraced(t *testing.T) {
	if _, ok := traceSource("PASS src/jobs/send-push.spec.ts", reportSrc); ok {
		t.Error("text the source does not have was traced")
	}
}

// y in the transcript copies the selection when there is one, and the answer
// in view when there is not.
func TestYCopiesTheSelection(t *testing.T) {
	m := newTestModel(t)
	withReply(m, reportSrc)
	left, top, w, _ := chatBox(t, m)
	from, _ := findText(t, m, "FE: đã ship")
	to, _ := findText(t, m, "BE: touchpoint")
	selecting(t, m, left, top+from, left+w-1, top+to)
	m.setFocus(focusChat)

	m.onKey(key("y"))
	if !strings.Contains(m.notice, "the selection") {
		t.Errorf("y with a selection: notice %q", m.notice)
	}
	m.clearSelection()
	m.onKey(key("y"))
	if !strings.Contains(m.notice, "the answer in view") {
		t.Errorf("y without a selection: notice %q", m.notice)
	}
}
