package vault

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"fmt"
	"strings"
	"time"

	"github.com/helldriver666/prism/internal/types"
)

const tokenPrefix = "prism_tok_"

// generateToken creates a new random token with the prism_tok_ prefix.
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("vault: token generation: %w", err)
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	return tokenPrefix + strings.ToLower(encoded), nil
}

// computeHMAC computes HMAC-SHA256 of data using the given secret.
// Used for token lookup in DB to prevent timing leaks.
func computeHMAC(secret, data []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(data)
	return mac.Sum(nil)
}

// registerProject creates a new project token with the given allowed providers and TTL.
func (v *vaultImpl) RegisterProject(projectID string, allowedProviders []types.ProviderID, tokenTTL time.Duration) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.closed {
		return "", ErrVaultClosed
	}

	token, err := generateToken()
	if err != nil {
		return "", err
	}

	hmacKey := computeHMAC(v.hmacSecret, []byte(token))

	providers := make([]string, len(allowedProviders))
	for i, p := range allowedProviders {
		providers[i] = string(p)
	}
	allowedStr := strings.Join(providers, ",")

	now := time.Now().Unix()
	var expiresAt int64
	if tokenTTL > 0 {
		expiresAt = now + int64(tokenTTL.Seconds())
	}

	if err := v.store.putToken(hmacKey, projectID, allowedStr, now, expiresAt); err != nil {
		return "", err
	}

	return token, nil
}

// ValidateToken validates a token using HMAC lookup + constant-time compare.
func (v *vaultImpl) ValidateToken(token string) (string, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if v.closed {
		return "", ErrVaultClosed
	}

	// 1. Compute HMAC(token) for DB lookup.
	hmacKey := computeHMAC(v.hmacSecret, []byte(token))

	// 2. Lookup by HMAC key.
	record, err := v.store.getTokenByHMAC(hmacKey)
	if err != nil {
		return "", ErrInvalidToken
	}

	// 3. Check TTL.
	if record.ExpiresAt > 0 && time.Now().Unix() > record.ExpiresAt {
		return "", ErrTokenExpired
	}

	// 4. Constant-time compare HMAC (final verification).
	if subtle.ConstantTimeCompare(hmacKey, record.HMACKey) != 1 {
		return "", ErrInvalidToken
	}

	return record.ProjectID, nil
}

// RevokeToken invalidates a token immediately.
func (v *vaultImpl) RevokeToken(token string) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.closed {
		return ErrVaultClosed
	}

	hmacKey := computeHMAC(v.hmacSecret, []byte(token))
	return v.store.deleteTokenByHMAC(hmacKey)
}
