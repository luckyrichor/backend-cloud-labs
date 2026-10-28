package audit

import (
	"syscall"
	"unsafe"
)

// Now uses the host-wide CLOCK_MONOTONIC epoch, shared by all Linux processes.
// Never compare values across different boot IDs.
func Now() int64 {
	var ts syscall.Timespec
	_, _, errno := syscall.RawSyscall(syscall.SYS_CLOCK_GETTIME, 1, uintptr(unsafe.Pointer(&ts)), 0)
	if errno != 0 {
		panic("CLOCK_MONOTONIC unavailable")
	}
	return ts.Nano()
}
