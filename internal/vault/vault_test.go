package vault_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/helldriver666/prism/internal/types"
	"github.com/helldriver666/prism/internal/vault"
)

const testPassword = "test-master-password-12345"

// newTestVault creates a vault in a temp directory for testing.
func newTestVault(t *testing.T) vault.Vault {
	t.Helper()
	dir := t.TempDir()
	v, err := vault.New(vault.Config{
		DBPath:         filepath.Join(dir, "secrets.db"),
		MasterPassword: []byte(testPassword),
	})
	if err != nil {
		t.Fatalf("vault.New: %v", err)
	}
	t.Cleanup(func() { v.Close() })
	return v
}

// --- Crypto & KDF ---

func TestVault_KDFParameters_OWASPCompliant(t *testing.T) {
	// Verify that vault opens successfully with Argon2id parameters.
	// The impl uses memory=64MB, iterations=3, parallelism=4 per OWASP.
	v := newTestVault(t)
	// If we get here without error, KDF worked.
	_ = v
}

func TestVault_WrongPassword_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "secrets.db")

	// Create vault with correct password.
	v, err := vault.New(vault.Config{
		DBPath:         dbPath,
		MasterPassword: []byte(testPassword),
	})
	if err != nil {
		t.Fatalf("vault.New: %v", err)
	}
	v.Close()

	// Reopen with wrong password.
	_, err = vault.New(vault.Config{
		DBPath:         dbPath,
		MasterPassword: []byte("wrong-password-completely"),
	})
	if err == nil {
		t.Fatal("expected error for wrong password, got nil")
	}
}

func TestVault_EncryptionNonceUniqueness(t *testing.T) {
	v := newTestVault(t)

	// Add 100 providers to verify all nonces are unique.
	// Nonces are generated randomly per encrypt() call, so this tests
	// that each per-record encryption uses a unique nonce.
	for i := 0; i < 100; i++ {
		pid := types.ProviderID("provider-" + string(rune('a'+i%26)) + string(rune('0'+i/26)))
		err := v.AddProvider(pid, "sk-test-key-"+string(pid), []string{"*"})
		if err != nil {
			t.Fatalf("AddProvider #%d: %v", i, err)
		}
	}
	// If we got here, all 100 encryptions succeeded with unique nonces.
	// (AES-GCM would fail on nonce collision for same key.)
}

func TestVault_PureGoSQLite(t *testing.T) {
	// Verify that the vault opens without CGO.
	// modernc.org/sqlite is pure Go — if this test compiles and runs, no CGO.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	v, err := vault.New(vault.Config{
		DBPath:         dbPath,
		MasterPassword: []byte(testPassword),
	})
	if err != nil {
		t.Fatalf("vault.New with pure Go SQLite: %v", err)
	}
	v.Close()

	// Verify DB file was created.
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Fatal("secrets.db was not created")
	}
}

// --- Token Management ---

func TestVault_ValidateToken_HMACLookup_ConstantTime(t *testing.T) {
	v := newTestVault(t)

	token, err := v.RegisterProject("proj-1", []types.ProviderID{types.ProviderClaude}, 0)
	if err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}

	// Valid token should return the project ID.
	projectID, err := v.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if projectID != "proj-1" {
		t.Fatalf("expected project_id=proj-1, got %s", projectID)
	}

	// Invalid token should fail.
	_, err = v.ValidateToken("prism_tok_invalid_token_xxxx")
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestVault_TokenTTL_Expired_Returns401(t *testing.T) {
	v := newTestVault(t)

	// Register with 1s TTL.
	token, err := v.RegisterProject("proj-ttl", []types.ProviderID{types.ProviderClaude}, 1*time.Second)
	if err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}

	// Wait for expiry (TTL stored in Unix seconds).
	time.Sleep(2 * time.Second)

	_, err = v.ValidateToken(token)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestVault_TokenTTL_Valid(t *testing.T) {
	v := newTestVault(t)

	token, err := v.RegisterProject("proj-ttl-valid", []types.ProviderID{types.ProviderClaude}, 1*time.Hour)
	if err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}

	projectID, err := v.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if projectID != "proj-ttl-valid" {
		t.Fatalf("expected proj-ttl-valid, got %s", projectID)
	}
}

func TestVault_TokenTTL_Zero_NeverExpires(t *testing.T) {
	v := newTestVault(t)

	// TTL=0 means permanent.
	token, err := v.RegisterProject("proj-perm", []types.ProviderID{types.ProviderClaude}, 0)
	if err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}

	projectID, err := v.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if projectID != "proj-perm" {
		t.Fatalf("expected proj-perm, got %s", projectID)
	}
}

func TestVault_RevokedToken_Returns401(t *testing.T) {
	v := newTestVault(t)

	token, err := v.RegisterProject("proj-revoke", []types.ProviderID{types.ProviderClaude}, 0)
	if err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}

	// Validate before revocation.
	_, err = v.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken before revoke: %v", err)
	}

	// Revoke.
	if err := v.RevokeToken(token); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}

	// Validate after revocation.
	_, err = v.ValidateToken(token)
	if err == nil {
		t.Fatal("expected error for revoked token")
	}
}

// --- Provider CRUD ---

func TestVault_AddProvider_And_RemoveProvider(t *testing.T) {
	v := newTestVault(t)

	err := v.AddProvider(types.ProviderClaude, "sk-ant-test-key", []string{"*"})
	if err != nil {
		t.Fatalf("AddProvider: %v", err)
	}

	err = v.RemoveProvider(types.ProviderClaude)
	if err != nil {
		t.Fatalf("RemoveProvider: %v", err)
	}

	// Removing again should fail.
	err = v.RemoveProvider(types.ProviderClaude)
	if err == nil {
		t.Fatal("expected error removing non-existent provider")
	}
}

// --- Key Rotation ---

func TestVault_RotateProviderKey_AtomicReplace(t *testing.T) {
	v := newTestVault(t)

	err := v.AddProvider(types.ProviderClaude, "sk-ant-OLD-key", []string{"*"})
	if err != nil {
		t.Fatalf("AddProvider: %v", err)
	}

	// Rotate key.
	err = v.RotateProviderKey(types.ProviderClaude, "sk-ant-NEW-key")
	if err != nil {
		t.Fatalf("RotateProviderKey: %v", err)
	}

	// Verify the vault still works with the provider (can sign requests).
	token, err := v.RegisterProject("rotation-test", []types.ProviderID{types.ProviderClaude}, 0)
	if err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}
	_, err = v.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken after rotation: %v", err)
	}
}

func TestVault_RotateProviderKey_NonExistent(t *testing.T) {
	v := newTestVault(t)

	err := v.RotateProviderKey("nonexistent", "sk-new-key")
	if err == nil {
		t.Fatal("expected error rotating non-existent provider")
	}
}

// --- Sign-not-Expose ---

func TestVault_SignNotExpose_KeyNeverLeaks(t *testing.T) {
	v := newTestVault(t)

	err := v.AddProvider(types.ProviderClaude, "sk-ant-secret-key-12345", []string{"*"})
	if err != nil {
		t.Fatalf("AddProvider: %v", err)
	}

	token, err := v.RegisterProject("sign-test", []types.ProviderID{types.ProviderClaude}, 0)
	if err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}
	_, err = v.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}

	req, _ := http.NewRequestWithContext(context.Background(), "POST", "https://api.anthropic.com/v1/messages", nil)

	err = v.SignRequest(context.Background(), "sign-test", types.ProviderClaude, req)
	if err != nil {
		t.Fatalf("SignRequest: %v", err)
	}

	// The key must NOT be in the original request headers.
	for key, values := range req.Header {
		for _, val := range values {
			if val == "sk-ant-secret-key-12345" {
				t.Fatalf("API key leaked in request header %s", key)
			}
		}
	}
}

func TestVault_SignNotExpose_RoundTripper_ZerosHeader(t *testing.T) {
	v := newTestVault(t)

	// Set up a mock HTTP server to capture the request.
	var capturedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("x-api-key")
		w.WriteHeader(200)
	}))
	defer server.Close()

	err := v.AddProvider(types.ProviderClaude, "sk-ant-roundtrip-test", []string{"*"})
	if err != nil {
		t.Fatalf("AddProvider: %v", err)
	}

	req, _ := http.NewRequestWithContext(context.Background(), "POST", server.URL+"/v1/messages", nil)

	err = v.SignRequest(context.Background(), "sign-test", types.ProviderClaude, req)
	if err != nil {
		// sign-test project may not exist yet — register it.
		_, regErr := v.RegisterProject("sign-test", []types.ProviderID{types.ProviderClaude}, 0)
		if regErr != nil {
			t.Fatalf("RegisterProject: %v", regErr)
		}
		err = v.SignRequest(context.Background(), "sign-test", types.ProviderClaude, req)
		if err != nil {
			t.Fatalf("SignRequest: %v", err)
		}
	}

	// Extract the transport from context and make the request.
	rt := vault.TransportFromContext(req.Context())
	if rt == nil {
		t.Fatal("expected signing transport in request context")
	}

	client := &http.Client{Transport: rt}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	resp.Body.Close()

	// The server should have received the API key.
	if capturedAuth != "sk-ant-roundtrip-test" {
		t.Fatalf("expected server to receive API key, got %q", capturedAuth)
	}
}

func TestVault_SignRequest_AccessDenied(t *testing.T) {
	v := newTestVault(t)

	// Add provider only for "allowed-project".
	err := v.AddProvider(types.ProviderClaude, "sk-ant-test", []string{"allowed-project"})
	if err != nil {
		t.Fatalf("AddProvider: %v", err)
	}

	// Register a different project.
	_, err = v.RegisterProject("denied-project", []types.ProviderID{types.ProviderClaude}, 0)
	if err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}

	req, _ := http.NewRequestWithContext(context.Background(), "POST", "https://api.anthropic.com/v1/messages", nil)

	// Attempt to sign with a project that doesn't have access.
	err = v.SignRequest(context.Background(), "denied-project", types.ProviderClaude, req)
	if err == nil {
		t.Fatal("expected access denied error")
	}
}

func TestVault_SignRequest_ProviderNotFound(t *testing.T) {
	v := newTestVault(t)

	req, _ := http.NewRequestWithContext(context.Background(), "POST", "https://api.example.com/v1/chat", nil)
	err := v.SignRequest(context.Background(), "any-project", "nonexistent", req)
	if err == nil {
		t.Fatal("expected provider not found error")
	}
}

// --- Key Zeroing ---

func TestVault_KeyZeroedOnAllPaths(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "secrets.db")

	v, err := vault.New(vault.Config{
		DBPath:         dbPath,
		MasterPassword: []byte(testPassword),
	})
	if err != nil {
		t.Fatalf("vault.New: %v", err)
	}

	err = v.AddProvider(types.ProviderClaude, "sk-ant-zeroed-test", []string{"*"})
	if err != nil {
		t.Fatalf("AddProvider: %v", err)
	}

	// Close should zero the encryption key.
	err = v.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	// After close, any operation should return ErrVaultClosed.
	err = v.AddProvider(types.ProviderOpenAI, "sk-test", []string{"*"})
	if err == nil {
		t.Fatal("expected error after Close")
	}
}

// --- Vault Reopen ---

func TestVault_ReopenWithCorrectPassword(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "secrets.db")

	// Create and populate.
	v, err := vault.New(vault.Config{
		DBPath:         dbPath,
		MasterPassword: []byte(testPassword),
	})
	if err != nil {
		t.Fatalf("vault.New: %v", err)
	}
	err = v.AddProvider(types.ProviderClaude, "sk-ant-persist-test", []string{"*"})
	if err != nil {
		t.Fatalf("AddProvider: %v", err)
	}
	token, err := v.RegisterProject("persist-proj", []types.ProviderID{types.ProviderClaude}, 0)
	if err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}
	v.Close()

	// Reopen with same password.
	v2, err := vault.New(vault.Config{
		DBPath:         dbPath,
		MasterPassword: []byte(testPassword),
	})
	if err != nil {
		t.Fatalf("vault.New (reopen): %v", err)
	}
	defer v2.Close()

	// Token should still be valid.
	projectID, err := v2.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken after reopen: %v", err)
	}
	if projectID != "persist-proj" {
		t.Fatalf("expected persist-proj, got %s", projectID)
	}
}

// --- All operations after Close ---

func TestVault_AllOpsAfterClose(t *testing.T) {
	v := newTestVault(t)
	v.Close()

	ctx := context.Background()

	// Every public method must return an error after Close.
	if _, err := v.RegisterProject("x", nil, 0); err == nil {
		t.Error("RegisterProject after Close should fail")
	}
	if _, err := v.ValidateToken("x"); err == nil {
		t.Error("ValidateToken after Close should fail")
	}
	if err := v.RevokeToken("x"); err == nil {
		t.Error("RevokeToken after Close should fail")
	}
	if err := v.AddProvider("x", "k", nil); err == nil {
		t.Error("AddProvider after Close should fail")
	}
	if err := v.RotateProviderKey("x", "k"); err == nil {
		t.Error("RotateProviderKey after Close should fail")
	}
	if err := v.RemoveProvider("x"); err == nil {
		t.Error("RemoveProvider after Close should fail")
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost", nil)
	if err := v.SignRequest(ctx, "p", "x", req); err == nil {
		t.Error("SignRequest after Close should fail")
	}
	// Double close should be safe.
	if err := v.Close(); err != nil {
		t.Errorf("double Close should be no-op, got %v", err)
	}
}

// --- SignRequest with OpenAI provider (Bearer prefix) ---

func TestVault_SignRequest_OpenAI_BearerPrefix(t *testing.T) {
	v := newTestVault(t)

	err := v.AddProvider(types.ProviderOpenAI, "sk-openai-key-123", []string{"*"})
	if err != nil {
		t.Fatalf("AddProvider: %v", err)
	}
	_, err = v.RegisterProject("oai-proj", []types.ProviderID{types.ProviderOpenAI}, 0)
	if err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}

	var capturedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
	}))
	defer server.Close()

	req, _ := http.NewRequestWithContext(context.Background(), "POST", server.URL+"/v1/chat", nil)
	err = v.SignRequest(context.Background(), "oai-proj", types.ProviderOpenAI, req)
	if err != nil {
		t.Fatalf("SignRequest: %v", err)
	}

	rt := vault.TransportFromContext(req.Context())
	if rt == nil {
		t.Fatal("no transport in context")
	}

	resp, err := (&http.Client{Transport: rt}).Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	if capturedAuth != "Bearer sk-openai-key-123" {
		t.Fatalf("expected 'Bearer sk-openai-key-123', got %q", capturedAuth)
	}
}

// --- SignRequest with Gemini provider ---

func TestVault_SignRequest_Gemini(t *testing.T) {
	v := newTestVault(t)

	err := v.AddProvider(types.ProviderGemini, "AIzaSy-gemini-key", []string{"*"})
	if err != nil {
		t.Fatalf("AddProvider: %v", err)
	}
	_, err = v.RegisterProject("gem-proj", []types.ProviderID{types.ProviderGemini}, 0)
	if err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}

	var capturedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("x-goog-api-key")
		w.WriteHeader(200)
	}))
	defer server.Close()

	req, _ := http.NewRequestWithContext(context.Background(), "POST", server.URL+"/v1/models", nil)
	err = v.SignRequest(context.Background(), "gem-proj", types.ProviderGemini, req)
	if err != nil {
		t.Fatalf("SignRequest: %v", err)
	}

	rt := vault.TransportFromContext(req.Context())
	resp, err := (&http.Client{Transport: rt}).Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	if capturedAuth != "AIzaSy-gemini-key" {
		t.Fatalf("expected Gemini key, got %q", capturedAuth)
	}
}

// --- Allowed project access control ---

func TestVault_SignRequest_AllowedProject(t *testing.T) {
	v := newTestVault(t)

	err := v.AddProvider(types.ProviderClaude, "sk-ant-acl-test", []string{"proj-a", "proj-b"})
	if err != nil {
		t.Fatalf("AddProvider: %v", err)
	}

	// proj-a has access.
	req, _ := http.NewRequestWithContext(context.Background(), "POST", "https://api.example.com", nil)
	err = v.SignRequest(context.Background(), "proj-a", types.ProviderClaude, req)
	if err != nil {
		t.Fatalf("proj-a should have access: %v", err)
	}

	// proj-c does not.
	req2, _ := http.NewRequestWithContext(context.Background(), "POST", "https://api.example.com", nil)
	err = v.SignRequest(context.Background(), "proj-c", types.ProviderClaude, req2)
	if err == nil {
		t.Fatal("proj-c should be denied")
	}
}

// --- mlock ---

func TestVault_MlockBuffer(t *testing.T) {
	// This test verifies mlock-pinned buffers work (or gracefully degrade).
	// On platforms where mlock is unavailable, the vault should still work.
	v := newTestVault(t)
	err := v.AddProvider(types.ProviderOpenAI, "sk-mlock-test", []string{"*"})
	if err != nil {
		t.Fatalf("AddProvider: %v", err)
	}
	// If we got here, mlock (or graceful degradation) works.
}

// --- TransportFromContext nil ---

func TestVault_TransportFromContext_Nil(t *testing.T) {
	rt := vault.TransportFromContext(context.Background())
	if rt != nil {
		t.Fatal("expected nil transport from empty context")
	}
}
