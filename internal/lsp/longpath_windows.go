//go:build windows

package lsp

import "golang.org/x/sys/windows"

// longPath spells a Windows path in full: C:\Users\PHAN~1.NGO becomes
// C:\Users\phan.ngoc. The server answers in full spellings, so a root given
// in short ones would not prefix anything it says.
func longPath(p string) string {
	in, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return p
	}
	buf := make([]uint16, 1024)
	n, err := windows.GetLongPathName(in, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) > len(buf) {
		return p
	}
	return windows.UTF16ToString(buf[:n])
}
