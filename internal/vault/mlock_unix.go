//go:build !windows

package vault

import "golang.org/x/sys/unix"

// mlock pins the buffer in physical RAM using mlock(2).
func mlock(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	return unix.Mlock(b)
}

// munlock unlocks a previously locked buffer.
func munlock(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	return unix.Munlock(b)
}
