package vault_test

import (
	"context"
	"net/http"
	"time"

	"github.com/helldriver666/prism/internal/types"
	"github.com/helldriver666/prism/internal/vault"
)

// Compile-time check: mockVault implements vault.Vault.
var _ vault.Vault = (*mockVault)(nil)

type mockVault struct {
	signRequestFn      func(ctx context.Context, projectID string, providerID types.ProviderID, req *http.Request) error
	registerProjectFn  func(projectID string, allowedProviders []types.ProviderID, tokenTTL time.Duration) (string, error)
	validateTokenFn    func(token string) (string, error)
	revokeTokenFn      func(token string) error
	addProviderFn      func(providerID types.ProviderID, apiKey string, allowedProjects []string) error
	rotateProviderKeyFn func(providerID types.ProviderID, newAPIKey string) error
	removeProviderFn   func(providerID types.ProviderID) error
	closeFn            func() error
}

func (m *mockVault) SignRequest(ctx context.Context, projectID string, providerID types.ProviderID, req *http.Request) error {
	if m.signRequestFn != nil {
		return m.signRequestFn(ctx, projectID, providerID, req)
	}
	return nil
}

func (m *mockVault) RegisterProject(projectID string, allowedProviders []types.ProviderID, tokenTTL time.Duration) (string, error) {
	if m.registerProjectFn != nil {
		return m.registerProjectFn(projectID, allowedProviders, tokenTTL)
	}
	return "prism_tok_mock_token", nil
}

func (m *mockVault) ValidateToken(token string) (string, error) {
	if m.validateTokenFn != nil {
		return m.validateTokenFn(token)
	}
	return "test-project", nil
}

func (m *mockVault) RevokeToken(token string) error {
	if m.revokeTokenFn != nil {
		return m.revokeTokenFn(token)
	}
	return nil
}

func (m *mockVault) AddProvider(providerID types.ProviderID, apiKey string, allowedProjects []string) error {
	if m.addProviderFn != nil {
		return m.addProviderFn(providerID, apiKey, allowedProjects)
	}
	return nil
}

func (m *mockVault) RotateProviderKey(providerID types.ProviderID, newAPIKey string) error {
	if m.rotateProviderKeyFn != nil {
		return m.rotateProviderKeyFn(providerID, newAPIKey)
	}
	return nil
}

func (m *mockVault) RemoveProvider(providerID types.ProviderID) error {
	if m.removeProviderFn != nil {
		return m.removeProviderFn(providerID)
	}
	return nil
}

func (m *mockVault) Close() error {
	if m.closeFn != nil {
		return m.closeFn()
	}
	return nil
}
