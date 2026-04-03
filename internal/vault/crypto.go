package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"runtime"
)

const (
	nonceSize = 12 // AES-256-GCM standard nonce size
	keySize   = 32 // AES-256
)

var (
	errCiphertextTooShort = errors.New("vault: ciphertext too short")
	errInvalidKeySize     = errors.New("vault: invalid encryption key size")
)

// encrypt encrypts plaintext using AES-256-GCM with a random nonce.
// Returns nonce (12 bytes) || ciphertext.
// Crypto errors are fatal — no degraded mode.
func encrypt(key, plaintext []byte) ([]byte, error) {
	if len(key) != keySize {
		return nil, errInvalidKeySize
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("vault: aes.NewCipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("vault: cipher.NewGCM: %w", err)
	}

	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("vault: nonce generation: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

// decrypt decrypts data in format nonce (12 bytes) || ciphertext using AES-256-GCM.
// Crypto errors are fatal — no degraded mode.
func decrypt(key, data []byte) ([]byte, error) {
	if len(key) != keySize {
		return nil, errInvalidKeySize
	}

	if len(data) < nonceSize {
		return nil, errCiphertextTooShort
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("vault: aes.NewCipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("vault: cipher.NewGCM: %w", err)
	}

	nonce := data[:nonceSize]
	ciphertext := data[nonceSize:]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("vault: decryption failed: %w", err)
	}

	return plaintext, nil
}

// explicitBzero zeroes the buffer and prevents compiler optimization.
// IMPORTANT: this is a best-effort measure. Go GC is a concurrent collector,
// it may copy the object to a new memory address before explicitBzero is called.
// runtime.KeepAlive prevents compiler optimization but not GC.
// The real security boundary is process isolation + mlock, not memory zeroing alone.
// See docs/SECURITY.md → "Go Memory Model Limitations".
func explicitBzero(b []byte) {
	for i := range b {
		b[i] = 0
	}
	runtime.KeepAlive(b)
}
