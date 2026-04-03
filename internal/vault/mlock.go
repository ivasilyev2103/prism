package vault

import "log"

// mlockBuffer allocates a buffer and attempts to pin it in RAM.
// On supported platforms, this prevents the buffer from being swapped to disk
// and fixes its address so explicitBzero zeroes the only copy.
//
// If mlock is unavailable (ulimit, platform), logs a warning and returns
// a regular heap buffer. This is best-effort — the real security boundary
// is process isolation (mTLS + loopback binding).
func mlockBuffer(size int) []byte {
	buf := make([]byte, size)
	if err := mlock(buf); err != nil {
		log.Printf("vault: WARNING: mlock unavailable (%v), using heap buffer", err)
	}
	return buf
}

// munlockBuffer unlocks and zeroes a previously mlocked buffer.
func munlockBuffer(buf []byte) {
	explicitBzero(buf)
	_ = munlock(buf)
}
