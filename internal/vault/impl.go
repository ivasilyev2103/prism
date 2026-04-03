package vault

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/helldriver666/prism/internal/types"
)

// Config holds the configuration for creating a new Vault.
type Config struct {
	DBPath         string
	MasterPassword []byte
}

// vaultImpl is the concrete implementation of the Vault interface.
type vaultImpl struct {
	mu            sync.RWMutex
	store         *storage
	encryptionKey []byte // mlock-pinned, zeroed on Close
	hmacSecret    []byte // derived separately from encryption key via HKDF-like approach
	closed        bool
}

// New creates a new Vault instance.
// Opens (or creates) the SQLite database, derives the encryption key from the master password.
// The master password is zeroed after key derivation.
func New(cfg Config) (Vault, error) {
	store, err := openStorage(cfg.DBPath)
	if err != nil {
		return nil, err
	}

	// Get or create salt.
	salt, err := store.getSalt()
	if err != nil {
		store.close()
		return nil, err
	}

	isNew := salt == nil
	if isNew {
		salt, err = generateSalt()
		if err != nil {
			store.close()
			return nil, err
		}
		if err := store.setSalt(salt); err != nil {
			store.close()
			return nil, err
		}
	}

	// Derive encryption key in mlock-pinned buffer.
	rawKey := deriveKey(cfg.MasterPassword, salt)
	encKey := mlockBuffer(keySize)
	copy(encKey, rawKey)
	explicitBzero(rawKey)

	// Zero master password — caller should not use it after this point.
	explicitBzero(cfg.MasterPassword)

	// Derive a separate HMAC secret for token lookup (different domain separation).
	// Use a simple approach: HMAC(encKey, "prism-token-hmac") as domain-separated key.
	hmacSecret := computeHMAC(encKey, []byte("prism-token-hmac"))

	// Verify password on existing DB by attempting a dummy operation.
	if !isNew {
		if err := verifyPassword(store, encKey); err != nil {
			munlockBuffer(encKey)
			explicitBzero(hmacSecret)
			store.close()
			return nil, err
		}
	} else {
		// Store a verification record for future password checks.
		if err := storeVerificationRecord(store, encKey); err != nil {
			munlockBuffer(encKey)
			explicitBzero(hmacSecret)
			store.close()
			return nil, err
		}
	}

	return &vaultImpl{
		store:         store,
		encryptionKey: encKey,
		hmacSecret:    hmacSecret,
	}, nil
}

// verifyPassword checks the master password against a stored verification record.
func verifyPassword(store *storage, encKey []byte) error {
	var data []byte
	err := store.db.QueryRow("SELECT value FROM metadata WHERE key = 'verify'").Scan(&data)
	if err != nil {
		return ErrWrongPassword
	}
	_, err = decrypt(encKey, data)
	if err != nil {
		return ErrWrongPassword
	}
	return nil
}

// storeVerificationRecord stores an encrypted known value for password verification.
func storeVerificationRecord(store *storage, encKey []byte) error {
	// Encrypt a known plaintext for later password verification.
	verifyPlaintext := []byte("prism-vault-verify")
	encrypted, err := encrypt(encKey, verifyPlaintext)
	if err != nil {
		return fmt.Errorf("vault: store verification: %w", err)
	}
	_, err = store.db.Exec(
		"INSERT OR REPLACE INTO metadata (key, value) VALUES ('verify', ?)", encrypted)
	return err
}

// SignRequest adds an Authorization header to an outgoing HTTP request.
// Uses Sign-not-Expose: the key is injected via a custom RoundTripper
// and zeroed after wire write.
func (v *vaultImpl) SignRequest(ctx context.Context, projectID string, providerID types.ProviderID, req *http.Request) error {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if v.closed {
		return ErrVaultClosed
	}

	// 1. Check that projectID has access to providerID.
	if err := v.checkAccess(projectID, providerID); err != nil {
		return err
	}

	// 2. Decrypt the provider API key.
	apiKey, err := v.getDecryptedKey(providerID)
	if err != nil {
		return err
	}

	// 3. Build the header value.
	pid := string(providerID)
	headerVal := authHeaderValue(pid, apiKey)
	explicitBzero(apiKey)

	// 4. Create signing transport and attach to the request's context.
	hk := authHeaderKey(pid)
	rt := newSigningTransport(http.DefaultTransport, hk, headerVal)

	// Inject the header directly — the signing RoundTripper pattern
	// is used by the Policy Engine when it creates the HTTP client.
	// For backward compat, we set a transport reference on the request context.
	*req = *req.WithContext(withTransport(ctx, rt))

	return nil
}

// AddProvider stores a provider API key (encrypted).
func (v *vaultImpl) AddProvider(providerID types.ProviderID, apiKey string, allowedProjects []string) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.closed {
		return ErrVaultClosed
	}

	keyBytes := []byte(apiKey)
	defer explicitBzero(keyBytes)

	encrypted, err := encrypt(v.encryptionKey, keyBytes)
	if err != nil {
		return err
	}

	allowedStr := strings.Join(allowedProjects, ",")
	return v.store.putProvider(string(providerID), encrypted, allowedStr, time.Now().Unix())
}

// RotateProviderKey replaces a provider API key atomically.
func (v *vaultImpl) RotateProviderKey(providerID types.ProviderID, newAPIKey string) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.closed {
		return ErrVaultClosed
	}

	pid := string(providerID)
	if !v.store.providerExists(pid) {
		return ErrProviderNotFound
	}

	keyBytes := []byte(newAPIKey)
	defer explicitBzero(keyBytes)

	encrypted, err := encrypt(v.encryptionKey, keyBytes)
	if err != nil {
		return err
	}

	return v.store.updateProviderKey(pid, encrypted, time.Now().Unix())
}

// RemoveProvider deletes a provider API key.
func (v *vaultImpl) RemoveProvider(providerID types.ProviderID) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.closed {
		return ErrVaultClosed
	}

	return v.store.deleteProvider(string(providerID))
}

// Close gracefully shuts down the Vault, zeroing encryption keys from RAM.
func (v *vaultImpl) Close() error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.closed {
		return nil
	}
	v.closed = true

	// Zero encryption key (mlock-pinned).
	munlockBuffer(v.encryptionKey)
	v.encryptionKey = nil

	// Zero HMAC secret.
	explicitBzero(v.hmacSecret)
	v.hmacSecret = nil

	return v.store.close()
}

// --- internal helpers ---

// checkAccess verifies that projectID has access to providerID.
func (v *vaultImpl) checkAccess(projectID string, providerID types.ProviderID) error {
	_, allowedProjects, err := v.store.getProvider(string(providerID))
	if err != nil {
		return err
	}

	if allowedProjects == "*" {
		return nil
	}

	for _, p := range strings.Split(allowedProjects, ",") {
		if strings.TrimSpace(p) == projectID {
			return nil
		}
	}

	return ErrAccessDenied
}

// getDecryptedKey decrypts and returns the provider API key.
// The caller must zero the returned bytes after use.
func (v *vaultImpl) getDecryptedKey(providerID types.ProviderID) ([]byte, error) {
	encryptedKey, _, err := v.store.getProvider(string(providerID))
	if err != nil {
		return nil, err
	}

	plaintext, err := decrypt(v.encryptionKey, encryptedKey)
	if err != nil {
		return nil, fmt.Errorf("vault: decrypt provider key: %w", err)
	}

	return plaintext, nil
}

// --- transport context key ---

type transportKeyType struct{}

var transportKey = transportKeyType{}

// withTransport attaches a signing RoundTripper to a context.
func withTransport(ctx context.Context, rt http.RoundTripper) context.Context {
	return context.WithValue(ctx, transportKey, rt)
}

// TransportFromContext extracts the signing RoundTripper from a context.
// Returns nil if no transport is set.
func TransportFromContext(ctx context.Context) http.RoundTripper {
	rt, _ := ctx.Value(transportKey).(http.RoundTripper)
	return rt
}

