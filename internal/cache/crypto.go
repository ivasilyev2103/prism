package cache

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
)

// deriveEntryKey derives a per-entry encryption key from the master key and entry ID.
// Uses SHA-256(masterKey || entryID) — simple HKDF-like derivation.
func deriveEntryKey(masterKey []byte, entryID string) []byte {
	h := sha256.New()
	h.Write(masterKey)
	h.Write([]byte(entryID))
	return h.Sum(nil) // 32 bytes = AES-256
}

// encryptPIIMapping encrypts data with AES-256-GCM using a per-entry key.
func encryptPIIMapping(masterKey []byte, entryID string, plaintext []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, nil
	}
	key := deriveEntryKey(masterKey, entryID)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("cache crypto: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cache crypto: new gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("cache crypto: nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// decryptPIIMapping decrypts data encrypted with encryptPIIMapping.
func decryptPIIMapping(masterKey []byte, entryID string, ciphertext []byte) ([]byte, error) {
	if len(ciphertext) == 0 {
		return nil, nil
	}
	key := deriveEntryKey(masterKey, entryID)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("cache crypto: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cache crypto: new gcm: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("cache crypto: ciphertext too short")
	}
	return gcm.Open(nil, ciphertext[:nonceSize], ciphertext[nonceSize:], nil)
}
