package main

import (
	"image/color"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The frame around the window.
//
// Windows 11 draws the title bar, and by default draws it light, which puts a
// white strip above a dark screen. These ask it, through the desktop window
// manager, for a dark title bar in exactly the screen's background colour and
// for the rounded corners Windows 11 windows have — so the title bar reads as
// the top of the same surface rather than as a lid on it. On a Windows that
// does not know an attribute the call fails and the default stands; nothing
// here can stop the window from opening.

const (
	dwmwaUseImmersiveDarkMode = 20
	dwmwaWindowCornerPref     = 33
	dwmwaBorderColor          = 34
	dwmwaCaptionColor         = 35
	dwmwaTextColor            = 36

	dwmwcpRound = 2
)

var procSetAttr = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")

func setAttr(hwnd uintptr, attr uint32, v uint32) {
	if procSetAttr.Find() != nil {
		return
	}
	_, _, _ = procSetAttr.Call(hwnd, uintptr(attr), uintptr(unsafe.Pointer(&v)), unsafe.Sizeof(v))
}

// colorref is a colour as Windows writes it: 0x00BBGGRR.
func colorref(c color.NRGBA) uint32 {
	return uint32(c.R) | uint32(c.G)<<8 | uint32(c.B)<<16
}

var (
	user32          = windows.NewLazySystemDLL("user32.dll")
	procSysParams   = user32.NewProc("SystemParametersInfoW")
	procDpiForSys   = user32.NewProc("GetDpiForSystem")
	spiGetWorkArea  = uintptr(0x0030)
	defaultDpi      = 96.0
	firstRunPercent = 0.82
)

// firstRunSize is the window's size the first time it opens, in dp: most of
// the screen's work area — the part the taskbar leaves — so it neither opens
// as a postage stamp nor under the taskbar, which a fixed default does on a
// laptop at 125% scaling. ok is false when the screen cannot be measured.
func firstRunSize() (w, h int, ok bool) {
	if procSysParams.Find() != nil {
		return 0, 0, false
	}
	var r struct{ L, T, R, B int32 }
	if ret, _, _ := procSysParams.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&r)), 0); ret == 0 {
		return 0, 0, false
	}
	scale := 1.0
	if procDpiForSys.Find() == nil {
		if dpi, _, _ := procDpiForSys.Call(); dpi != 0 {
			scale = float64(dpi) / defaultDpi
		}
	}
	w = int(float64(r.R-r.L) * firstRunPercent / scale)
	h = int(float64(r.B-r.T) * firstRunPercent / scale)
	return w, h, w > 0 && h > 0
}

var (
	procGetWindowRect = user32.NewProc("GetWindowRect")
	procSetWindowPos  = user32.NewProc("SetWindowPos")
)

// centerWindow puts the window in the middle of the work area. Left to
// itself Windows cascades a new window down and to the right, which on a
// short screen puts its bottom edge — the prompt — under the taskbar.
func centerWindow(hwnd uintptr) {
	var wa, wr struct{ L, T, R, B int32 }
	if procSysParams.Find() != nil || procGetWindowRect.Find() != nil || procSetWindowPos.Find() != nil {
		return
	}
	if ret, _, _ := procSysParams.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&wa)), 0); ret == 0 {
		return
	}
	if ret, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&wr))); ret == 0 {
		return
	}
	w, h := wr.R-wr.L, wr.B-wr.T
	x := wa.L + max(0, (wa.R-wa.L-w)/2)
	y := wa.T + max(0, (wa.B-wa.T-h)/2)
	// Asynchronous, and not as a nicety. This runs on the goroutine handling
	// Gio's events, while the window's own thread waits for it to finish
	// handling one; a synchronous SetWindowPos sends messages to that thread
	// and waits for them, and the two waited on each other for ever — the
	// window opened, drew nothing more, and could not be closed. With this
	// flag the move is posted to the window's thread and this call returns.
	const swpNoSize, swpNoZOrder, swpNoActivate, swpAsync = 0x0001, 0x0004, 0x0010, 0x4000
	_, _, _ = procSetWindowPos.Call(hwnd, 0, uintptr(x), uintptr(y), 0, 0,
		swpNoSize|swpNoZOrder|swpNoActivate|swpAsync)
}

// wheelNotch is one detent of a mouse wheel in the units Windows reports it
// in (WHEEL_DELTA). Gio passes those units through as they are: they are not
// pixels.
const wheelNotch = 120

// wheelLines is how many lines one notch scrolls, as set in Windows' mouse
// settings — the number the console, and so PowerShell, scrolls by. A
// "one screen at a time" setting comes back as a sentinel; it is read as a
// generous jump rather than as four billion lines.
func wheelLines() int {
	const spiGetWheelScrollLines = 0x0068
	var n uint32
	if procSysParams.Find() != nil {
		return 3
	}
	if ret, _, _ := procSysParams.Call(spiGetWheelScrollLines, 0, uintptr(unsafe.Pointer(&n)), 0); ret == 0 {
		return 3
	}
	switch {
	case n == 0:
		return 0
	case n > 100:
		return 30
	}
	return int(n)
}

func styleWindow(hwnd uintptr, bg color.NRGBA) {
	setAttr(hwnd, dwmwaUseImmersiveDarkMode, 1)
	setAttr(hwnd, dwmwaCaptionColor, colorref(bg))
	setAttr(hwnd, dwmwaBorderColor, colorref(bg))
	setAttr(hwnd, dwmwaTextColor, colorref(rgb(0xb9c0cc)))
	setAttr(hwnd, dwmwaWindowCornerPref, dwmwcpRound)
}
