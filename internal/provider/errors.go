package provider

import "errors"

var (
	// ErrNotImplemented is returned by stub providers whose Execute is not yet implemented.
	ErrNotImplemented = errors.New("provider: not implemented")
)
