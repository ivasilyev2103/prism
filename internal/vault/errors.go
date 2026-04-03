package vault

import "errors"

var (
	ErrProviderNotFound = errors.New("vault: provider not found")
	ErrAccessDenied     = errors.New("vault: access denied")
	ErrInvalidToken     = errors.New("vault: invalid token")
	ErrTokenExpired     = errors.New("vault: token expired")
	ErrVaultClosed      = errors.New("vault: vault is closed")
	ErrWrongPassword    = errors.New("vault: wrong master password")
)
