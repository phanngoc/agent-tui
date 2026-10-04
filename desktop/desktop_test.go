package main

import (
	"fmt"
	"image"
	"strings"
	"testing"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	uv "github.com/charmbracelet/ultraviolet"
)

// ---- input -----------------------------------------------------------------

// TestTelexBecomesWhatUnikeyWouldType: an input method edits what was typed;
// the core only understands keystrokes. Each edit has to turn into the
// backspaces and characters that make the same change.
func TestTelexBecomesWhatUnikeyWouldType(t *testing.T) {
	var b imeBuffer
	type step struct {
		ev        key.EditEvent
		back      int
		insert    string
		bufferNow string
	}
	for i, s := range []step{
		{key.EditEvent{Range: key.Range{Start: 0, End: 0}, Text: "x"}, 0, "x", "x"},
		{key.EditEvent{Range: key.Range{Start: 1, End: 1}, Text: "i"}, 0, "i", "xi"},
		{key.EditEvent{Range: key.Range{Start: 2, End: 2}, Text: "n"}, 0, "n", "xin"},
		{key.EditEvent{Range: key.Range{Start: 3, End: 3}, Text: " c"}, 0, " c", "xin c"},
		{key.EditEvent{Range: key.Range{Start: 5, End: 5}, Text: "ha"}, 0, "ha", "xin cha"},
		// Telex: "chaof" — the input method replaces "a" with "ào".
		{key.EditEvent{Range: key.Range{Start: 6, End: 7}, Text: "ào"}, 1, "ào", "xin chào"},
	} {
		back, insert := b.apply(s.ev)
		if back != s.back || insert != s.insert || string(b.text) != s.bufferNow {
			t.Errorf("step %d: back=%d insert=%q buffer=%q, want %d %q %q",
				i, back, insert, string(b.text), s.back, s.insert, s.bufferNow)
		}
	}
}

// TestEditInTheMiddleRetypesTheTail: an input method may replace text that is
// not at the end; what follows it has to be typed again after the change.
func TestEditInTheMiddleRetypesTheTail(t *testing.T) {
	b := imeBuffer{text: []rune("abcd")}
	back, insert := b.apply(key.EditEvent{Range: key.Range{Start: 1, End: 2}, Text: "X"})
	if back != 3 || insert != "Xcd" || string(b.text) != "aXcd" {
		t.Errorf("back=%d insert=%q buffer=%q", back, insert, string(b.text))
	}
}

func TestIMEBufferStaysShort(t *testing.T) {
	var b imeBuffer
	for i := 0; i < 100; i++ {
		b.apply(key.EditEvent{Range: key.Range{Start: b.len(), End: b.len()}, Text: "a"})
	}
	if b.len() != imeKeep {
		t.Errorf("buffer grew to %d", b.len())
	}
}

func TestKeysMapToTerminalKeys(t *testing.T) {
	for _, tc := range []struct {
		ev   key.Event
		want uv.KeyPressEvent
	}{
		{key.Event{Name: key.NameReturn}, uv.KeyPressEvent{Code: uv.KeyEnter}},
		{key.Event{Name: key.NameTab, Modifiers: key.ModShift}, uv.KeyPressEvent{Code: uv.KeyTab, Mod: uv.ModShift}},
		{key.Event{Name: key.NameUpArrow, Modifiers: key.ModAlt}, uv.KeyPressEvent{Code: uv.KeyUp, Mod: uv.ModAlt}},
		{key.Event{Name: "K", Modifiers: key.ModCtrl}, uv.KeyPressEvent{Code: 'k', Mod: uv.ModCtrl}},
		{key.Event{Name: key.NameSpace, Modifiers: key.ModCtrl}, uv.KeyPressEvent{Code: uv.KeySpace, Mod: uv.ModCtrl}},
		{key.Event{Name: key.NameF1}, uv.KeyPressEvent{Code: uv.KeyF1}},
	} {
		got, ok := keyEvent(tc.ev)
		if !ok || got != tc.want {
			t.Errorf("%+v -> %+v (ok=%v), want %+v", tc.ev, got, ok, tc.want)
		}
	}
}

// TestCtrlKReachesTheCoreAsAControlCode goes through the emulator: what the
// core reads for ctrl+k must be the byte a terminal sends, 0x0b.
func TestCtrlKReachesTheCoreAsAControlCode(t *testing.T) {
	tm := NewTerm(20, 4, oneDark.Fg, oneDark.Bg)
	defer tm.Close()
	k, _ := keyEvent(key.Event{Name: "K", Modifiers: key.ModCtrl})
	go tm.Key(k)
	buf := make([]byte, 8)
	n, _ := tm.Read(buf)
	if got := string(buf[:n]); got != "\x0b" {
		t.Errorf("ctrl+k sent %q", got)
	}
}

func TestCoreEnvDropsTheLaunchingTerminal(t *testing.T) {
	env := coreEnv([]string{"PATH=x", "NO_COLOR=1", "TERM=dumb", "no_color=1", "HOME=h", "COLORTERM=8bit"})
	got := strings.Join(env, " ")
	if got != "PATH=x HOME=h" {
		t.Errorf("env = %q", got)
	}
}

// ---- drawing ---------------------------------------------------------------

func testContext() layout.Context {
	return layout.Context{
		Ops:         new(op.Ops),
		Metric:      unit.Metric{PxPerDp: 1.25, PxPerSp: 1.25},
		Constraints: layout.Exact(image.Pt(1600, 1000)),
	}
}

// screenful writes a screen that looks like agent-tui: borders, colour, and
// a line of Vietnamese on every row.
func screenful(tm *Term, cols, rows int) {
	var b strings.Builder
	b.WriteString("\x1b[H")
	for y := 0; y < rows; y++ {
		fmt.Fprintf(&b, "\x1b[%d;1H\x1b[38;2;97;175;239m│\x1b[0m row %03d · điều tra bug trên aws sbi dev \x1b[1mbold\x1b[0m ─────", y+1, y)
	}
	tm.Write([]byte(b.String()))
}

func newTestGrid(t testing.TB, cols, rows int) (*Grid, *Term) {
	t.Helper()
	g := NewGrid(loadFaces(), oneDark)
	g.SetPx(16)
	tm := NewTerm(cols, rows, oneDark.Fg, oneDark.Bg)
	screenful(tm, cols, rows)
	return g, tm
}

// TestUnchangedRowsAreNotDrawnAgain is the performance contract: a frame in
// which one row changed records one row.
func TestUnchangedRowsAreNotDrawnAgain(t *testing.T) {
	g, tm := newTestGrid(t, 160, 48)
	gtx := testContext()
	g.Layout(gtx, tm, true)
	before := len(g.rows)
	if before == 0 {
		t.Fatal("nothing was recorded")
	}

	tm.Write([]byte("\x1b[10;5Hstreaming…"))
	recorded := 0
	gtx.Ops.Reset()
	keys := append([]uint64(nil), g.keys...)
	g.Layout(gtx, tm, true)
	for i, k := range g.keys {
		if k != keys[i] {
			recorded++
		}
	}
	if recorded != 1 {
		t.Errorf("one row changed and %d rows were recorded again", recorded)
	}
}

// TestScrolledRowsReuseTheirRecordings: the cache is by content, not row, so
// a screen that scrolled by one line records one new line.
func TestScrolledRowsReuseTheirRecordings(t *testing.T) {
	g, tm := newTestGrid(t, 120, 30)
	gtx := testContext()
	g.Layout(gtx, tm, true)
	known := map[uint64]bool{}
	for _, k := range g.keys {
		known[k] = true
	}
	tm.Write([]byte("\x1b[30;1H\n\x1b[30;1Ha new line at the bottom"))
	gtx.Ops.Reset()
	g.Layout(gtx, tm, true)
	fresh := 0
	for _, k := range g.keys {
		if !known[k] {
			fresh++
		}
	}
	if fresh > 2 {
		t.Errorf("scrolling one line produced %d new rows to draw", fresh)
	}
}

func TestCJKFaceIsLoadedOnlyWhenNeeded(t *testing.T) {
	g, tm := newTestGrid(t, 80, 10)
	gtx := testContext()
	g.Layout(gtx, tm, true)
	if g.cjk {
		t.Fatal("the CJK face was loaded for a screen without CJK")
	}
	tm.Write([]byte("\x1b[2;1H本人確認へ進む"))
	gtx.Ops.Reset()
	g.Layout(gtx, tm, true)
	if !g.cjk {
		t.Error("the CJK face was not loaded when Japanese appeared")
	}
}

// ---- benchmarks ------------------------------------------------------------

// BenchmarkFrameIdle is a frame with nothing new: the cursor blinking, the
// mouse moving.
func BenchmarkFrameIdle(b *testing.B) {
	g, tm := newTestGrid(b, 200, 60)
	gtx := testContext()
	g.Layout(gtx, tm, true)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		gtx.Ops.Reset()
		g.Layout(gtx, tm, true)
	}
}

// BenchmarkFrameStreaming is a frame during an answer: one row changed.
func BenchmarkFrameStreaming(b *testing.B) {
	g, tm := newTestGrid(b, 200, 60)
	gtx := testContext()
	g.Layout(gtx, tm, true)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tm.Write([]byte(fmt.Sprintf("\x1b[40;5Htoken %d of the answer", i)))
		gtx.Ops.Reset()
		g.Layout(gtx, tm, true)
	}
}

// BenchmarkFrameFullRedraw is the worst case: every row new, as on a switch
// to another session.
func BenchmarkFrameFullRedraw(b *testing.B) {
	g, tm := newTestGrid(b, 200, 60)
	gtx := testContext()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.rows = make(map[uint64]*rowRec)
		g.lastGen = 0
		gtx.Ops.Reset()
		g.Layout(gtx, tm, true)
	}
}

// ---- scrolling ---------------------------------------------------------------

func TestWheelStepsKeepTheRemainder(t *testing.T) {
	var acc float32
	if n := wheelSteps(&acc, 30, 12); n != 2 || acc != 6 {
		t.Errorf("30px at 12px a step: n=%d acc=%v, want 2 and 6 left", n, acc)
	}
	if n := wheelSteps(&acc, 7, 12); n != 1 || acc != 1 {
		t.Errorf("the remainder was not carried: n=%d acc=%v", n, acc)
	}
	if n := wheelSteps(&acc, -25, 12); n != -2 {
		t.Errorf("upward travel: n=%d acc=%v", n, acc)
	}
}

// TestSidewaysReachesTheCoreAsShiftWheel: Gio hands shift+wheel over as
// horizontal travel with the shift taken off. The core scrolls its preview
// sideways on shift+wheel, which is what Windows Terminal sends, so that is
// what horizontal travel must become on the wire.
func TestSidewaysReachesTheCoreAsShiftWheel(t *testing.T) {
	tm := NewTerm(80, 24, oneDark.Fg, oneDark.Bg)
	defer tm.Close()
	// The core turns on button-event mouse tracking with SGR encoding.
	tm.Write([]byte("\x1b[?1002h\x1b[?1006h"))
	s := &shell{term: tm}
	go s.wheel(image.Pt(10, 5), uv.ModShift, 1)
	buf := make([]byte, 32)
	n, _ := tm.Read(buf)
	// SGR: button 65 (wheel down) + 4 (shift) = 69, at 1-based column 11, row 6.
	if got := string(buf[:n]); got != "\x1b[<69;11;6M" {
		t.Errorf("shift+wheel down sent %q", got)
	}
}

// TestOneNotchIsOneJump: the wheel scrolls the way the console does — a
// notch is a single jump of a few lines, not a run of small steps.
func TestOneNotchIsOneJump(t *testing.T) {
	var acc float32
	if n := wheelSteps(&acc, 120, wheelNotch); n != 1 {
		t.Errorf("one notch made %d steps, want 1", n)
	}
	if n := wheelSteps(&acc, -120, wheelNotch); n != -1 {
		t.Errorf("one notch up made %d steps, want -1", n)
	}
}

// TestFineDeltasDoNotDrift: a high-resolution wheel or a touchpad reports a
// notch as several small deltas. They add up to one jump when a notch's
// worth has arrived, and to nothing before — no creeping a line at a time.
func TestFineDeltasDoNotDrift(t *testing.T) {
	var acc float32
	got := []int{}
	for _, d := range []float32{40, 40, 40, 15, 15} {
		got = append(got, wheelSteps(&acc, d, wheelNotch))
	}
	want := []int{0, 0, 1, 0, 0}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("steps per delta = %v, want %v", got, want)
		}
	}
}

// TestNotchEventsFollowWindowsSettings: lines per notch, as set in Windows,
// over the three the core scrolls per wheel event.
func TestNotchEventsFollowWindowsSettings(t *testing.T) {
	for lines, want := range map[int]int{0: 0, 1: 1, 3: 1, 5: 2, 6: 2, 9: 3, 30: 10} {
		if got := notchEvents(lines); got != want {
			t.Errorf("%d lines a notch -> %d events, want %d", lines, got, want)
		}
	}
	if n := wheelLines(); n < 0 {
		t.Errorf("wheelLines = %d", n)
	}
}

// TestModifiedKeysReachTheCore: the emulator wrote nothing for a named key
// held with Ctrl or Shift, so deleting and moving by word, and selecting in
// the prompt, did nothing in the window. The sequences are the ones a probe in
// a pseudo-console showed arriving as the right keys.
func TestModifiedKeysReachTheCore(t *testing.T) {
	cases := []struct {
		k    uv.KeyPressEvent
		want string
	}{
		{uv.KeyPressEvent{Code: uv.KeyDelete, Mod: uv.ModCtrl}, "\x1b[3;5~"},
		{uv.KeyPressEvent{Code: uv.KeyLeft, Mod: uv.ModCtrl}, "\x1b[1;5D"},
		{uv.KeyPressEvent{Code: uv.KeyRight, Mod: uv.ModShift}, "\x1b[1;2C"},
		{uv.KeyPressEvent{Code: uv.KeyHome, Mod: uv.ModCtrl | uv.ModShift}, "\x1b[1;6H"},
		{uv.KeyPressEvent{Code: uv.KeyPgDown, Mod: uv.ModCtrl}, "\x1b[6;5~"},
		{uv.KeyPressEvent{Code: uv.KeyF5, Mod: uv.ModShift}, "\x1b[15;2~"},
		{uv.KeyPressEvent{Code: uv.KeyBackspace, Mod: uv.ModCtrl}, "\x1b[8;14;8;1;8;1_"},
		{uv.KeyPressEvent{Code: uv.KeyBackspace, Mod: uv.ModShift}, "\x7f"},
		{uv.KeyPressEvent{Code: uv.KeyEnter, Mod: uv.ModShift}, "\x1b\r"},
	}
	for _, c := range cases {
		got, ok := modifiedKey(c.k)
		if !ok || got != c.want {
			t.Errorf("%v: got %q ok=%v, want %q", c.k, got, ok, c.want)
		}
	}
	// Plain keys and Alt alone are the emulator's to encode.
	for _, k := range []uv.KeyPressEvent{
		{Code: uv.KeyDelete}, {Code: uv.KeyUp, Mod: uv.ModAlt}, {Code: 'a', Mod: uv.ModCtrl},
	} {
		if got, ok := modifiedKey(k); ok {
			t.Errorf("%v was encoded here as %q", k, got)
		}
	}
}
