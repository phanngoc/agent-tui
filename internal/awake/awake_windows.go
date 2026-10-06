//go:build windows

package awake

import (
	"errors"
	"runtime"

	"golang.org/x/sys/windows"
)

// The execution state a thread can ask for. ES_CONTINUOUS makes it last
// until the same thread says otherwise, or ends.
const (
	esContinuous      = 0x80000000
	esSystemRequired  = 0x00000001
	esDisplayRequired = 0x00000002
)

var setThreadExecutionState = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetThreadExecutionState")

// winHolder keeps one OS thread for the state: Windows ties it to the thread
// that asked, and a goroutine wanders between threads.
type winHolder struct {
	ask chan uint32
	err chan error
}

func newHolder() (holder, string) {
	if setThreadExecutionState.Find() != nil {
		return nil, "SetThreadExecutionState (unavailable)"
	}
	h := &winHolder{ask: make(chan uint32), err: make(chan error)}
	go func() {
		runtime.LockOSThread() // never unlocked: the thread is this state's
		for flags := range h.ask {
			r, _, err := setThreadExecutionState.Call(uintptr(flags))
			if r == 0 {
				h.err <- errors.Join(errors.New("SetThreadExecutionState failed"), err)
				continue
			}
			h.err <- nil
		}
	}()
	return h, "SetThreadExecutionState"
}

func (h *winHolder) hold(display bool) error {
	flags := uint32(esContinuous | esSystemRequired)
	if display {
		flags |= esDisplayRequired
	}
	h.ask <- flags
	return <-h.err
}

func (h *winHolder) release() {
	h.ask <- esContinuous
	<-h.err
}
