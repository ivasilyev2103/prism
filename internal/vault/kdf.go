package vault

import (
	"crypto/rand"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters per OWASP recommendations.
const (
	argon2Time    = 3         // iterations
	argon2Memory  = 64 * 1024 // 64 MB
	argon2Threads = 4         // parallelism
	argon2KeyLen  = 32        // AES-256
	saltSize      = 32
)

// deriveKey derives an encryption key from password and salt using Argon2id.
// The returned key should be stored in an mlock-pinned buffer and zeroed on Close.
func deriveKey(password, salt []byte) []byte {
	return argon2.IDKey(password, salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)
}

// generateSalt creates a cryptographically random 32-byte salt.
func generateSalt() ([]byte, error) {
	salt := make([]byte, saltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("vault: salt generation: %w", err)
	}
	return salt, nil
}
