package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"time"
)

// diagnostics turns on what the environment asks for. Neither costs anything
// when unset.
//
// AGENT_TUI_DESKTOP_MEM writes the Go heap.s numbers and a heap profile to
// %TEMP% six seconds after start: that is how the process.s memory was split
// between what this program allocates and what the graphics driver does.
//
// AGENT_TUI_DESKTOP_STACKS writes every goroutine.s stack there every two
// seconds: that is how a hang was found to be two threads waiting on each
// other, rather than guessed at.
func diagnostics() {
	if os.Getenv("AGENT_TUI_DESKTOP_STACKS") != "" {
		go func() {
			for {
				time.Sleep(2 * time.Second)
				if f, err := os.Create(filepath.Join(os.TempDir(), "agent-tui-desktop-stacks.txt")); err == nil {
					_ = pprof.Lookup("goroutine").WriteTo(f, 2)
					f.Close()
				}
			}
		}()
	}
	if os.Getenv("AGENT_TUI_DESKTOP_MEM") == "" {
		return
	}
	go func() {
		time.Sleep(6 * time.Second)
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		s := fmt.Sprintf("heap_alloc=%dMB heap_sys=%dMB sys=%dMB gc=%d\n",
			m.HeapAlloc>>20, m.HeapSys>>20, m.Sys>>20, m.NumGC)
		_ = os.WriteFile(filepath.Join(os.TempDir(), "agent-tui-desktop-mem.txt"), []byte(s), 0o644)
		if f, err := os.Create(filepath.Join(os.TempDir(), "agent-tui-desktop-heap.pprof")); err == nil {
			_ = pprof.WriteHeapProfile(f)
			f.Close()
		}
	}()
}
