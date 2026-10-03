# agent-tui desktop

agent-tui in a native Windows 11 window. Open **agent-tui** from the Start
menu, or run `agent-tui-desktop` with the same flags as `agent-tui` (for example
`-C ~/code/x`).

It does not change agent-tui. The core runs exactly as it does in a terminal,
unmodified, in a Windows pseudo-console. This program is the terminal: the
window, the font, the drawing, the keyboard and the mouse. It is written in
Go, and you need nothing but Go to build it.

```
agent-tui.exe ── unchanged, the same binary `tui` runs
      ⇅  ConPTY  (the pseudo-console Windows Terminal hosts shells in)
agent-tui-desktop.exe
   ├─ x/vt   VT emulator in Go: output → a grid of cells; keys and mouse → bytes
   └─ Gio    GPU drawing (Direct3D 11), no cgo, no web view
```

## Can Go do cross-platform UI?

Yes, but the right choice depends on constraints, and on this machine the
constraint is that there is no C compiler. CGO is off.

| | Native look | Needs cgo on Windows | Footprint | Fit here |
|---|---|---|---|---|
| **Gio** | drawn by itself, on the GPU | **no** | ~8 MB binary | ✅ chosen |
| Fyne | its own widgets, OpenGL | yes | ~25 MB | ✗ no C compiler |
| Wails | HTML/CSS in WebView2 | no | 10 MB + a Chromium process | works, but a browser for a terminal |
| walk / lxn/win | real Win32 controls | no | small | Windows only, no GPU text grid |

Gio is pure Go on Windows. It talks to Direct3D 11 through syscalls, and the
same code builds for macOS and Linux (those need cgo for their system
libraries). Wails was the first plan and was dropped: it means a Chromium
renderer, and roughly 100 MB more memory, to draw a grid of characters.

## Performance

The unit of work is the row. A row's cells are hashed, and a row whose hash has
been drawn before replays the GPU operations recorded the first time: no
shaping, no layout. The hash is of the row's content, not of its position, so a
screen that scrolls one line draws one line. When nothing was written since the
last frame, the rows are not even hashed.

Measured on a 200 × 60 grid (`go test -bench . ./desktop`):

| frame | time | allocations |
|---|---|---|
| nothing changed | ~2 µs | 0 |
| streaming an answer (one row changed) | ~0.15–0.5 ms | ~110 |
| full redraw (switching session) | ~4 ms | ~1.6k |

A 60 fps frame is 16.6 ms. Hashing a full screen costs about 11 ns a cell, and
most of that is reading the cells.

Memory is mostly fonts, and fonts are loaded when needed. Cascadia Mono and
Segoe UI Symbol load at start. MS Gothic, for Japanese, loads the first time a
CJK character appears: parsing all three of its faces up front was 45 MB of a
56 MB heap on a screen with no Japanese on it. Expect about 130–220 MB private,
most of it the Direct3D driver's, and about 15 MB of Go heap.

## What it gets right

- **Colour:** 24-bit, with the 16 named colours from agent-tui's One Dark
  palette. It drops `NO_COLOR` and `TERM=dumb` inherited from the shell that
  launched it, because those describe that terminal, not this one.
- **Text:** ASCII is shaped in runs. Box drawing, Vietnamese and Japanese are
  placed cell by cell, so a glyph from a fallback face can never push a line off
  the grid. Bold is drawn twice a fraction of a pixel apart, and italic is
  sheared, because Cascadia Mono on Windows is one variable file.
- **Input methods:** Telex, VNI and Japanese IMEs edit a short buffer. Each edit
  becomes the backspaces and characters that make the same change, which is
  what Unikey sends and what the core's prompt understands. The IME window
  follows the cursor the core draws.
- **Keys and mouse:** encoded by the emulator according to the modes the core
  set: SGR mouse, application cursor keys, bracketed paste.
  - `Ctrl+V` pastes text, or passes `Ctrl+V` through so the core can attach a
    picture.
  - Right click pastes.
  - `Shift`+wheel, a tilting wheel or a sideways trackpad swipe scrolls the
    preview sideways. Gio reports all three as horizontal travel with the
    shift removed, so the window passes it on as shift+wheel, which is what
    Windows Terminal sends.
  - `Ctrl+=` / `Ctrl+-` / `Ctrl+0` zoom the font.
  - `F11` toggles full screen.
- **Lifetime:**
  - The core is in a job object that kills it when this process ends, however
    it ends, so a crashed or killed window never leaves an agent running
    unseen.
  - Closing the window takes about 200 ms.
  - When the core exits, a card offers to start it again.
- **Window:**
  - Dark title bar in the screen's own colour, and Windows 11 rounded corners.
  - First size is 82% of the work area, centred.
  - Size, maximised state and font size are remembered in
    `~/.local/share/agent-tui/desktop.json`.

## Build

```powershell
go build -C desktop -trimpath -ldflags "-s -w -H windowsgui" -o bin\agent-tui-desktop.exe .
```

The deploy skill builds it into `go\bin` beside `agent-tui.exe` and adds the
Start menu entry. It finds the core beside itself, then on `PATH`, or wherever
`AGENT_TUI_CORE` points.

The icon is drawn in Go (`go run ./tools/mkicon`). The `.syso` holding it and
the version information is generated with
`go run github.com/tc-hib/go-winres@v0.3.3 simply --icon icon.png --manifest gui …`.
Both are committed, so a build needs nothing else.

Diagnostics: `AGENT_TUI_DESKTOP_MEM=1` writes heap numbers and a heap profile to
`%TEMP%` after six seconds. `AGENT_TUI_DESKTOP_STACKS=1` writes every
goroutine's stack there every two seconds. That is how a deadlock between a
synchronous `SetWindowPos` and Gio's event delivery was found; it is now
asynchronous.
