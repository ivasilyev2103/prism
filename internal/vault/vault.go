package vault

import (
	"context"
	"net/http"
	"time"

	"github.com/helldriver666/prism/internal/types"
)

// Vault manages secrets and signs outgoing requests.
// IMPORTANT: plaintext keys never leave Vault through any method.
//
// Go memory model limitation: explicit_bzero is best-effort.
// GC may copy data before zeroing. The real security boundary is
// process isolation (mTLS + loopback binding), not memory zeroing.
// See docs/SECURITY.md → "Go Memory Model Limitations".
type Vault interface {
	// SignRequest adds an Authorization header to an outgoing HTTP request.
	// The key is not returned to the caller — only used internally.
	// projectID and providerID are used for access control checks.
	//
	// Implementation: custom http.RoundTripper injects the header at transport level
	// and zeroes it after wire write. See internal/vault/SPEC.md.
	SignRequest(ctx context.Context, projectID string, providerID types.ProviderID, req *http.Request) error

	// RegisterProject registers an application and returns a local token.
	// allowedProviders is a list of providers accessible to this project.
	// tokenTTL is the token lifetime; 0 = permanent.
	RegisterProject(projectID string, allowedProviders []types.ProviderID, tokenTTL time.Duration) (token string, err error)

	// ValidateToken validates a local token and returns the projectID.
	// Uses HMAC(token) for DB lookup + constant-time compare.
	ValidateToken(token string) (projectID string, err error)

	// RevokeToken invalidates a token immediately.
	RevokeToken(token string) error

	// AddProvider stores a provider API key (encrypted).
	AddProvider(providerID types.ProviderID, apiKey string, allowedProjects []string) error

	// RotateProviderKey replaces a provider API key.
	// The old key is immediately removed, the new key is stored.
	RotateProviderKey(providerID types.ProviderID, newAPIKey string) error

	// RemoveProvider deletes a provider API key.
	RemoveProvider(providerID types.ProviderID) error

	// Close gracefully shuts down the Vault, zeroing encryption keys from RAM.
	Close() error
}
