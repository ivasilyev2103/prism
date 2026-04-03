package vault

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	kernel32    = syscall.NewLazyDLL("kernel32.dll")
	virtualLock   = kernel32.NewProc("VirtualLock")
	virtualUnlock = kernel32.NewProc("VirtualUnlock")
)

// mlock pins the buffer in physical RAM using VirtualLock on Windows.
func mlock(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	ret, _, err := virtualLock.Call(
		uintptr(unsafe.Pointer(&b[0])),
		uintptr(len(b)),
	)
	if ret == 0 {
		return fmt.Errorf("VirtualLock: %w", err)
	}
	return nil
}

// munlock unlocks a previously locked buffer.
func munlock(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	ret, _, err := virtualUnlock.Call(
		uintptr(unsafe.Pointer(&b[0])),
		uintptr(len(b)),
	)
	if ret == 0 {
		return fmt.Errorf("VirtualUnlock: %w", err)
	}
	return nil
}
