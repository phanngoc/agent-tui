//go:build !windows

package lsp

func longPath(p string) string { return p }
