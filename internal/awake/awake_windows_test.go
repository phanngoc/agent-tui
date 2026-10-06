//go:build windows

package awake

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// systemState is the execution state Windows holds for the whole system,
// every thread's requests together.
func systemState(t *testing.T) uint32 {
	t.Helper()
	var state uint32
	r, _, err := windows.NewLazySystemDLL("powrprof.dll").NewProc("CallNtPowerInformation").Call(
		16, 0, 0, uintptr(unsafe.Pointer(&state)), unsafe.Sizeof(state)) // 16: SystemExecutionState
	if r != 0 {
		t.Fatalf("CallNtPowerInformation: %x %v", r, err)
	}
	return state
}

// Windows itself says the machine is required while the keeper holds it,
// and the screen too when asked.
func TestWindowsSeesTheRequest(t *testing.T) {
	k := New()
	k.Set(ModeBusy, true, true, "test")
	defer k.Close()
	st := systemState(t)
	if st&esSystemRequired == 0 || st&esDisplayRequired == 0 {
		t.Fatalf("system execution state %#x: the request is not seen", st)
	}
	k.Set(ModeBusy, false, true, "")
	t.Logf("held %#x, released %#x", st, systemState(t))
}
