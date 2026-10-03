package main

import (
	"encoding/binary"
	"hash/maphash"
	"image"
	"image/color"
	"math"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/image/math/fixed"
)

// Drawing the grid, fast.
//
// A frame is the whole screen, and the screen is a grid of a few thousand
// cells, of which a streaming answer changes two or three rows at a time. So
// the unit of work is the row, and a row is drawn once: its cells are hashed,
// and a row whose hash has been drawn before replays the operations recorded
// the first time — no shaping, no layout, a single call. That holds when the
// screen scrolls, too: the hash is of the row's content, not of its position,
// so a line that moved up one row is the same recording drawn one row higher.
//
// When nothing was written since the last frame, the rows are not even hashed:
// the previous frame's list of them is drawn again.
//
// Within a row, text is drawn the way a terminal lays it out rather than the
// way a text engine would: every run starts at its own column. Plain ASCII of
// one style is shaped as one run; anything else — box drawing, Vietnamese,
// Japanese — is placed cell by cell, so a glyph from a fallback face with an
// advance of its own can never push the rest of the line off the grid.

// Grid draws a Term.
type Grid struct {
	shaper *text.Shaper
	faces  []font.FontFace
	cjk    bool // the CJK face has been loaded
	pal    Palette

	// Metrics, in pixels, for the size the font is at now.
	px       int
	cellW    float32
	cellH    int
	baseline float32
	ascent   float32

	rows    map[uint64]*rowRec
	clock   uint64 // frames drawn, for evicting old recordings
	lastGen uint64
	keys    []uint64

	glyphs []text.Glyph
	keyBuf []byte
	cells  []uv.Cell
}

type rowRec struct {
	ops  op.Ops
	call op.CallOp
	used uint64
}

// NewGrid makes a grid drawing in the given faces and colours.
func NewGrid(faces []font.FontFace, pal Palette) *Grid {
	g := &Grid{faces: faces, pal: pal, rows: make(map[uint64]*rowRec)}
	g.shaper = text.NewShaper(text.NoSystemFonts(), text.WithCollection(faces))
	freeMemory()
	return g
}

// needCJK reports whether any of these rows has a character the CJK face is
// for, and it is not loaded yet.
func (g *Grid) needCJK(rows []pendingRow) bool {
	if g.cjk {
		return false
	}
	for _, p := range rows {
		for i := range p.cells {
			c := p.cells[i].Content
			if len(c) > 1 { // CJK is never a single byte
				for _, r := range c {
					if isCJK(r) {
						return true
					}
				}
			}
		}
	}
	return false
}

// addCJK loads the CJK face and makes a shaper that can use it. Rows already
// recorded keep their recordings: their paths belong to them, not to the
// shaper that made them, and none of them had a CJK character in it.
func (g *Grid) addCJK() {
	g.cjk = true
	if extra := loadCJK(); len(extra) > 0 {
		g.faces = append(g.faces, extra...)
		g.shaper = text.NewShaper(text.NoSystemFonts(), text.WithCollection(g.faces))
		freeMemory()
	}
}

// SetPx sets the font size in pixels, measuring the cell it makes. Every
// recording is dropped: it was drawn at the old size.
func (g *Grid) SetPx(px int) {
	if px == g.px {
		return
	}
	g.px = px
	g.shaper.LayoutString(g.params(false), "M")
	gl, ok := g.shaper.NextGlyph()
	for ok {
		if _, more := g.shaper.NextGlyph(); !more {
			break
		}
	}
	if !ok || gl.Advance == 0 {
		// No face at all; a cell is then the conventional 0.6em by 1.2em.
		g.cellW, g.ascent = float32(px)*0.6, float32(px)*0.8
		g.cellH = int(math.Ceil(float64(px) * 1.2))
	} else {
		g.cellW = fx(gl.Advance)
		g.ascent = fx(gl.Ascent)
		h := fx(gl.Ascent) + fx(gl.Descent)
		// A little leading, as a terminal has: lines of box drawing still
		// touch, and lines of text do not crowd.
		g.cellH = int(math.Ceil(float64(h) * 1.08))
	}
	g.baseline = float32(math.Round(float64((float32(g.cellH)-(g.ascent+fx(gl.Descent)))/2 + g.ascent)))
	g.rows = make(map[uint64]*rowRec)
	g.keys = g.keys[:0]
	g.lastGen = 0
}

// CellSize is one cell, in pixels: fractional across, whole down.
func (g *Grid) CellSize() (float32, int) { return g.cellW, g.cellH }

func fx(v fixed.Int26_6) float32 { return float32(v) / 64 }

func (g *Grid) params(bold bool) text.Parameters {
	f := font.Font{Typeface: monoFace}
	if bold {
		f.Weight = font.Bold
	}
	return text.Parameters{
		Font:     f,
		PxPerEm:  fixed.I(g.px),
		MaxWidth: 1 << 24,
		MaxLines: 1,
	}
}

// Layout draws the screen with its top-left corner at the origin of gtx.
func (g *Grid) Layout(gtx layout.Context, t *Term, focused bool) {
	g.clock++

	// Under the lock: hash the rows, and copy out the cells of the ones never
	// drawn before. Shaping happens after it is released, so the reader that
	// feeds the screen is never kept waiting on a frame.
	t.mu.Lock()
	cols, rows := t.cols, t.rows
	cur := t.emu.CursorPosition()
	showCursor := t.cursorVisible
	var missing []pendingRow
	if t.gen != g.lastGen || len(g.keys) != rows {
		g.lastGen = t.gen
		g.keys = g.keys[:0]
		for y := 0; y < rows; y++ {
			k := g.rowKey(t, y, cols)
			g.keys = append(g.keys, k)
			if _, ok := g.rows[k]; !ok {
				missing = append(missing, pendingRow{key: k, cells: copyRow(t, y, cols)})
				// A blank row of one key can appear many times on one screen;
				// it is recorded once.
				g.rows[k] = nil
			}
		}
	}
	t.mu.Unlock()

	if g.needCJK(missing) {
		g.addCJK()
	}
	for _, p := range missing {
		g.rows[p.key] = g.record(p.cells)
	}

	for y, k := range g.keys {
		rec := g.rows[k]
		if rec == nil {
			continue
		}
		rec.used = g.clock
		off := op.Offset(image.Pt(0, y*g.cellH)).Push(gtx.Ops)
		rec.call.Add(gtx.Ops)
		off.Pop()
	}

	if showCursor && cur.X >= 0 && cur.X < cols && cur.Y >= 0 && cur.Y < rows {
		g.cursor(gtx, cur.X, cur.Y, focused)
	}
	g.evict(rows)
}

// rowKey hashes everything that decides how a row looks. Positions are not in
// it, which is what lets a scrolled line find its old recording.
//
// It runs over every cell of every row whenever anything was written, so it
// is the one loop here that has to be cheap: maphash rather than a hash.Hash
// behind an interface, and colours packed by their concrete type rather than
// through RGBA, which is a call through an interface and a conversion each.
func (g *Grid) rowKey(t *Term, y, cols int) uint64 {
	// The row is laid out in one reused buffer and hashed in one call: a
	// call per cell cost more than the hashing itself.
	b := g.keyBuf[:0]
	for x := 0; x < cols; x++ {
		c := t.emu.CellAt(x, y)
		if c == nil {
			b = append(b, 0)
			continue
		}
		b = append(b, c.Content...)
		b = binary.LittleEndian.AppendUint32(b, colorKey(c.Style.Fg))
		b = binary.LittleEndian.AppendUint32(b, colorKey(c.Style.Bg))
		b = binary.LittleEndian.AppendUint32(b, colorKey(c.Style.UnderlineColor))
		b = append(b, byte(c.Width), c.Style.Attrs, byte(c.Style.Underline), 0xff)
	}
	g.keyBuf = b
	return maphash.Bytes(rowSeed, b)
}

var rowSeed = maphash.MakeSeed()

// colorKey packs a colour into 32 bits that differ whenever the colour does:
// the kind in the top byte, the value below it.
func colorKey(c color.Color) uint32 {
	switch v := c.(type) {
	case nil:
		return 0
	case color.RGBA:
		return 1<<24 | uint32(v.R)<<16 | uint32(v.G)<<8 | uint32(v.B)
	case ansi.BasicColor:
		return 2<<24 | uint32(v)
	case ansi.IndexedColor:
		return 3<<24 | uint32(v)
	}
	r, gg, b, _ := c.RGBA()
	return 4<<24 | (r>>8)<<16 | (gg>>8)<<8 | b>>8
}

type pendingRow struct {
	key   uint64
	cells []uv.Cell
}

func copyRow(t *Term, y, cols int) []uv.Cell {
	out := make([]uv.Cell, cols)
	for x := range out {
		if c := t.emu.CellAt(x, y); c != nil {
			out[x] = *c
		}
	}
	return out
}

// cellStyle is a cell's look, resolved to colours that can be painted.
type cellStyle struct {
	fg, bg            color.NRGBA
	bold, italic      bool
	underline, strike bool
	conceal           bool
}

func (g *Grid) styleOf(c *uv.Cell) cellStyle {
	s := c.Style
	fg := g.pal.resolve(s.Fg, g.pal.Fg)
	bg := g.pal.resolve(s.Bg, g.pal.Bg)
	if s.Attrs&uv.AttrReverse != 0 {
		fg, bg = bg, fg
	}
	if s.Attrs&uv.AttrFaint != 0 {
		fg = dim(fg, bg)
	}
	return cellStyle{
		fg: fg, bg: bg,
		bold:      s.Attrs&uv.AttrBold != 0,
		italic:    s.Attrs&uv.AttrItalic != 0,
		underline: s.Underline != uv.UnderlineNone,
		strike:    s.Attrs&uv.AttrStrikethrough != 0,
		conceal:   s.Attrs&uv.AttrConceal != 0,
	}
}

// record draws one row into a recording of its own, kept across frames.
func (g *Grid) record(cells []uv.Cell) *rowRec {
	rec := &rowRec{}
	m := op.Record(&rec.ops)
	ops := &rec.ops

	// Backgrounds first, merged into spans: a status bar is one rectangle,
	// not a hundred and sixty.
	spanStart, spanBg := -1, color.NRGBA{}
	flushSpan := func(end int) {
		if spanStart >= 0 && spanBg != g.pal.Bg {
			g.fill(ops, spanStart, end, spanBg)
		}
		spanStart = -1
	}
	for x := 0; x < len(cells); x++ {
		c := &cells[x]
		if c.Width == 0 && c.Content == "" && x > 0 {
			continue // the second half of a wide character
		}
		bg := g.styleOf(c).bg
		if spanStart < 0 || bg != spanBg {
			flushSpan(x)
			spanStart, spanBg = x, bg
		}
	}
	flushSpan(len(cells))

	// Then text, in runs.
	var run []byte
	runAt := 0
	var runStyle cellStyle
	flushRun := func() {
		if len(run) > 0 {
			g.text(ops, string(run), runAt, runStyle)
		}
		run = run[:0]
	}
	for x := 0; x < len(cells); x++ {
		c := &cells[x]
		w := max(c.Width, 1)
		st := g.styleOf(c)
		if st.underline || st.strike {
			g.decorate(ops, x, x+w, st)
		}
		content := c.Content
		if content == "" || content == " " || st.conceal {
			flushRun()
			x += w - 1
			continue
		}
		if len(content) == 1 && content[0] < 0x80 && w == 1 {
			if len(run) > 0 && st != runStyle {
				flushRun()
			}
			if len(run) == 0 {
				runAt, runStyle = x, st
			}
			run = append(run, content...)
			continue
		}
		flushRun()
		g.text(ops, content, x, st)
		x += w - 1
	}
	flushRun()

	rec.call = m.Stop()
	return rec
}

// fill paints the background of cells [from, to).
func (g *Grid) fill(ops *op.Ops, from, to int, c color.NRGBA) {
	x0 := int(math.Round(float64(float32(from) * g.cellW)))
	x1 := int(math.Round(float64(float32(to) * g.cellW)))
	paint.FillShape(ops, c, clip.Rect{Min: image.Pt(x0, 0), Max: image.Pt(x1, g.cellH)}.Op())
}

// decorate draws an underline or a line through cells [from, to).
func (g *Grid) decorate(ops *op.Ops, from, to int, st cellStyle) {
	x0 := int(math.Round(float64(float32(from) * g.cellW)))
	x1 := int(math.Round(float64(float32(to) * g.cellW)))
	thick := max(1, g.px/14)
	if st.underline {
		y := int(g.baseline) + max(1, g.px/10)
		paint.FillShape(ops, st.fg, clip.Rect{Min: image.Pt(x0, y), Max: image.Pt(x1, y+thick)}.Op())
	}
	if st.strike {
		y := int(g.baseline - g.ascent*0.3)
		paint.FillShape(ops, st.fg, clip.Rect{Min: image.Pt(x0, y), Max: image.Pt(x1, y+thick)}.Op())
	}
}

// text shapes s and paints it with its first cell at column col.
//
// Bold is drawn by drawing again a fraction of a pixel to the right, and
// italic by shearing, rather than by asking for faces that are not installed:
// Cascadia Mono on Windows is one variable file, and what it would fall back
// to is a different typeface in the middle of a line.
func (g *Grid) text(ops *op.Ops, s string, col int, st cellStyle) {
	g.shaper.LayoutString(g.params(false), s)
	g.glyphs = g.glyphs[:0]
	for {
		gl, ok := g.shaper.NextGlyph()
		if !ok {
			break
		}
		g.glyphs = append(g.glyphs, gl)
	}
	if len(g.glyphs) == 0 {
		return
	}
	path := g.shaper.Shape(g.glyphs)
	x := float32(col)*g.cellW + fx(g.glyphs[0].X)

	draw := func(dx float32) {
		tr := f32.AffineId()
		if st.italic {
			tr = tr.Shear(f32.Point{}, -0.2, 0)
		}
		tr = tr.Offset(f32.Pt(x+dx, g.baseline))
		t := op.Affine(tr).Push(ops)
		outline := clip.Outline{Path: path}.Op().Push(ops)
		paint.ColorOp{Color: st.fg}.Add(ops)
		paint.PaintOp{}.Add(ops)
		outline.Pop()
		t.Pop()
	}
	draw(0)
	if st.bold {
		draw(max(0.5, float32(g.px)/28))
	}
}

// cursor draws the caret. The core places the real cursor where typing goes,
// so this is the point an input method composes at, and it is drawn as a bar:
// a block would hide the character it sits on.
func (g *Grid) cursor(gtx layout.Context, x, y int, focused bool) {
	x0 := int(math.Round(float64(float32(x) * g.cellW)))
	w := max(2, g.px/8)
	c := g.pal.Cursor
	if !focused {
		c.A = 0x60
	}
	r := clip.Rect{Min: image.Pt(x0, y*g.cellH), Max: image.Pt(x0+w, (y+1)*g.cellH)}
	paint.FillShape(gtx.Ops, c, r.Op())
}

// evict drops recordings that have not been drawn for a while, keeping the
// cache at a few screens' worth.
func (g *Grid) evict(rows int) {
	limit := max(64, rows*4)
	if len(g.rows) <= limit {
		return
	}
	for k, r := range g.rows {
		if r == nil || g.clock-r.used > 2 {
			delete(g.rows, k)
		}
	}
}
