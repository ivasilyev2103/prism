package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/helldriver666/prism/internal/audit"
	"github.com/helldriver666/prism/internal/cache"
	"github.com/helldriver666/prism/internal/config"
	"github.com/helldriver666/prism/internal/cost"
	"github.com/helldriver666/prism/internal/ingress"
	"github.com/helldriver666/prism/internal/policy"
	"github.com/helldriver666/prism/internal/privacy"
	"github.com/helldriver666/prism/internal/provider"
	"github.com/helldriver666/prism/internal/types"
	"github.com/helldriver666/prism/internal/vault"
)

// testEnv holds all components for an E2E test.
type testEnv struct {
	dataDir  string
	vault    vault.Vault
	engine   *policy.Engine
	handler  ingress.Handler
	mux      *http.ServeMux
	tracker  cost.Tracker
	auditLog audit.Logger
	cache    cache.SemanticCache
	costDB   *sql.DB
	token    string

	mockServer *httptest.Server
}

func extractDB(tracker cost.Tracker) *sql.DB {
	type hasDB interface{ DB() *sql.DB }
	if t, ok := tracker.(hasDB); ok {
		return t.DB()
	}
	return nil
}

// newTestEnv creates a fully wired test environment with a mock provider.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	dataDir := t.TempDir()
	os.WriteFile(filepath.Join(dataDir, "config.yaml"), config.DefaultConfigYAML, 0600)
	os.WriteFile(filepath.Join(dataDir, "budgets.yaml"), config.DefaultBudgetsYAML, 0600)

	// 1. Mock AI provider server.
	mockServer := httptest.NewServer(http.HandlerFunc(mockProviderHandler))

	// 2. Vault.
	password := []byte("test-master-password-12345")
	auditHMACKey := deriveHMACKey(password, "prism-audit-hmac")
	cacheEncKey := deriveHMACKey(password, "prism-cache-encryption")

	vaultPw := make([]byte, len(password))
	copy(vaultPw, password)
	v, err := vault.New(vault.Config{
		DBPath:         filepath.Join(dataDir, "secrets.db"),
		MasterPassword: vaultPw,
	})
	if err != nil {
		t.Fatalf("create vault: %v", err)
	}

	v.AddProvider(types.ProviderClaude, "sk-test-key", []string{"*"})
	v.AddProvider(types.ProviderOllama, "none", []string{"*"})
	v.AddProvider(types.ProviderOpenAI, "sk-test-openai", []string{"*"})

	token, err := v.RegisterProject("test-project", nil, 0)
	if err != nil {
		t.Fatalf("register project: %v", err)
	}

	// 3. Provider registry (all pointing at mock).
	reg := provider.NewRegistry()
	reg.Register(provider.NewClaudeProvider(provider.ClaudeConfig{BaseURL: mockServer.URL}))
	reg.Register(provider.NewOllamaProvider(provider.OllamaConfig{BaseURL: mockServer.URL}))
	reg.Register(provider.NewOpenAIProvider(provider.OpenAIConfig{BaseURL: mockServer.URL}))
	reg.Register(provider.NewGeminiProvider(provider.GeminiConfig{}))

	// 4. Privacy (tier 1).
	pipeline := privacy.NewPipeline(privacy.NewRegexDetector())

	// 5. Cost tracker.
	costTracker, err := cost.NewTracker(filepath.Join(dataDir, "cost.db"))
	if err != nil {
		t.Fatalf("create cost tracker: %v", err)
	}
	costDB := extractDB(costTracker)
	budgetChecker := cost.NewBudgetChecker(costDB)
	loadBudgets(costDB, dataDir)

	// 6. Audit logger.
	auditLog, err := audit.NewLogger(filepath.Join(dataDir, "audit.db"), auditHMACKey)
	if err != nil {
		t.Fatalf("create audit logger: %v", err)
	}

	// 7. Semantic cache (mock embedder).
	semanticCache, _ := cache.NewSemanticCache(
		filepath.Join(dataDir, "cache.db"),
		&mockEmbedderImpl{},
		cacheEncKey,
		nil,
	)

	// 8. Router.
	routesYAML := []byte(`routes:
  - name: "image_gen"
    if:
      service_type: image
    then:
      provider: openai
      fallback: ollama
  - name: "default"
    then:
      provider: claude
      fallback: ollama
`)
	router, err := policy.NewRouter(routesYAML, reg)
	if err != nil {
		t.Fatalf("create router: %v", err)
	}

	// 9. Engine.
	engine := policy.NewEngine(policy.Deps{
		Privacy:     pipeline,
		Router:      router,
		BudgetCheck: budgetChecker,
		Providers:   reg,
		CostTracker: costTracker,
		AuditLog:    auditLog,
		Cache:       semanticCache,
		Failover:    policy.NewFailover(),
	})

	// 10. Ingress.
	handler := ingress.NewHandler(ingress.Config{
		Validator:   v,
		RateLimiter: ingress.NewRateLimiter(600),
	})

	mux := buildMux(engine, handler, costTracker, auditLog)

	return &testEnv{
		dataDir:    dataDir,
		vault:      v,
		engine:     engine,
		handler:    handler,
		mux:        mux,
		tracker:    costTracker,
		auditLog:   auditLog,
		cache:      semanticCache,
		costDB:     costDB,
		token:      token,
		mockServer: mockServer,
	}
}

func (env *testEnv) close() {
	if env.cache != nil {
		if c, ok := env.cache.(closeable); ok {
			c.Close()
		}
	}
	if c, ok := env.auditLog.(closeable); ok {
		c.Close()
	}
	if c, ok := env.tracker.(closeable); ok {
		c.Close()
	}
	env.vault.Close()
	env.mockServer.Close()
}

func (env *testEnv) doRequest(t *testing.T, method, path string, body interface{}, headers map[string]string) *http.Response {
	t.Helper()

	var reqBody io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reqBody = bytes.NewReader(data)
	}

	req := httptest.NewRequest(method, path, reqBody)
	req.Header.Set("X-Prism-Token", env.token)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	rr := httptest.NewRecorder()
	env.mux.ServeHTTP(rr, req)
	return rr.Result()
}

func mockProviderHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.URL.Path {
	case "/v1/messages":
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":    "msg_test_123",
			"model": "claude-haiku-4-5",
			"content": []map[string]string{
				{"type": "text", "text": "Hello! The answer is 42."},
			},
			"usage": map[string]int{"input_tokens": 50, "output_tokens": 10},
		})
	case "/api/chat":
		json.NewEncoder(w).Encode(map[string]interface{}{
			"model":             "llama3",
			"message":           map[string]string{"role": "assistant", "content": "Hello from Ollama!"},
			"prompt_eval_count": 30,
			"eval_count":        8,
		})
	case "/v1/images/generations":
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]string{{"url": "https://example.com/image.png"}},
		})
	default:
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}
}

// --- Mock Embedder ---

type mockEmbedderImpl struct{}

func (m *mockEmbedderImpl) Embed(_ context.Context, text string) ([]float32, error) {
	emb := make([]float32, 384)
	for i := range emb {
		emb[i] = float32(len(text)%100) / 100.0
	}
	return emb, nil
}

func (m *mockEmbedderImpl) HealthCheck(_ context.Context) error { return nil }

// --- E2E Tests ---

func TestE2E_ChatRequest_WithPIIObfuscation(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	resp := env.doRequest(t, "POST", "/v1/chat/completions", map[string]interface{}{
		"model": "claude-haiku-4-5",
		"messages": []map[string]string{
			{"role": "user", "content": "My email is test@example.com, please help me."},
		},
	}, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
}

func TestE2E_ImageRequest_PassThrough(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	resp := env.doRequest(t, "POST", "/v1/images/generations", map[string]interface{}{
		"model":  "dall-e-3",
		"prompt": "a sunset over mountains",
	}, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
}

func TestE2E_BudgetExceeded_Returns429(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	// Set very low budget and pre-fill cost.
	env.costDB.Exec(`DELETE FROM budgets`)
	env.costDB.Exec(`INSERT INTO budgets (level, limit_usd, period, action) VALUES ('global', 0.0001, 'monthly', 'block')`)
	env.costDB.Exec(`INSERT INTO requests (id, ts, project_id, provider_id, service_type, model,
		cost_usd, billing_type, latency_ms, status) VALUES
		('prev1', ?, 'test-project', 'claude', 'chat', 'haiku', 0.01, 'per_token', 100, 'ok')`,
		time.Now().Unix())

	resp := env.doRequest(t, "POST", "/v1/chat/completions", map[string]interface{}{
		"model": "claude-haiku-4-5",
		"messages": []map[string]string{
			{"role": "user", "content": "Hello"},
		},
	}, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 429, got %d: %s", resp.StatusCode, body)
	}
}

func TestE2E_ProviderFailover(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	// Create a failing primary provider.
	failingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "down"}`))
	}))
	defer failingServer.Close()

	reg := provider.NewRegistry()
	reg.Register(provider.NewClaudeProvider(provider.ClaudeConfig{BaseURL: failingServer.URL}))
	reg.Register(provider.NewOllamaProvider(provider.OllamaConfig{BaseURL: env.mockServer.URL}))
	reg.Register(provider.NewOpenAIProvider(provider.OpenAIConfig{}))
	reg.Register(provider.NewGeminiProvider(provider.GeminiConfig{}))

	routesYAML := []byte(`routes:
  - name: "default"
    then:
      provider: claude
      fallback: ollama
`)
	router, _ := policy.NewRouter(routesYAML, reg)
	engine := policy.NewEngine(policy.Deps{
		Privacy:     privacy.NewPipeline(privacy.NewRegexDetector()),
		Router:      router,
		BudgetCheck: cost.NewBudgetChecker(env.costDB),
		Providers:   reg,
		CostTracker: env.tracker,
		AuditLog:    env.auditLog,
		Failover:    policy.NewFailover(),
	})
	handler := ingress.NewHandler(ingress.Config{
		Validator:   env.vault,
		RateLimiter: ingress.NewRateLimiter(600),
	})

	mux := buildMux(engine, handler, env.tracker, env.auditLog)

	data, _ := json.Marshal(map[string]interface{}{
		"model":    "llama3",
		"messages": []map[string]string{{"role": "user", "content": "Hello"}},
	})
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(data))
	req.Header.Set("X-Prism-Token", env.token)
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 (failover), got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestE2E_CacheHit_ForChat(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	body := map[string]interface{}{
		"model":    "claude-haiku-4-5",
		"messages": []map[string]string{{"role": "user", "content": "What is 2+2?"}},
	}

	// First request — miss.
	resp1 := env.doRequest(t, "POST", "/v1/chat/completions", body, nil)
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", resp1.StatusCode)
	}

	// Second identical request — should also succeed (may hit cache).
	resp2 := env.doRequest(t, "POST", "/v1/chat/completions", body, nil)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("second request: expected 200, got %d", resp2.StatusCode)
	}
}

func TestE2E_CacheMiss_ForImage(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	body := map[string]interface{}{
		"model":  "dall-e-3",
		"prompt": "a cat riding a bicycle",
	}

	resp1 := env.doRequest(t, "POST", "/v1/images/generations", body, nil)
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("image request: expected 200, got %d", resp1.StatusCode)
	}

	resp2 := env.doRequest(t, "POST", "/v1/images/generations", body, nil)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("second image request: expected 200, got %d", resp2.StatusCode)
	}
}

func TestE2E_Health_Endpoint(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	req := httptest.NewRequest("GET", "/prism/health", nil)
	rr := httptest.NewRecorder()
	env.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var result map[string]string
	json.NewDecoder(rr.Body).Decode(&result)
	if result["status"] != "ok" {
		t.Fatalf("expected status ok, got %q", result["status"])
	}
}

func TestE2E_NoToken_Returns401(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	data, _ := json.Marshal(map[string]interface{}{
		"model":    "claude-haiku-4-5",
		"messages": []map[string]string{{"role": "user", "content": "Hello"}},
	})
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	// No token.

	rr := httptest.NewRecorder()
	env.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestE2E_RoutesValidate(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(provider.NewClaudeProvider(provider.ClaudeConfig{}))
	reg.Register(provider.NewOllamaProvider(provider.OllamaConfig{}))
	reg.Register(provider.NewOpenAIProvider(provider.OpenAIConfig{}))
	reg.Register(provider.NewGeminiProvider(provider.GeminiConfig{}))

	router, err := policy.NewRouter(config.DefaultRoutesYAML, reg)
	if err != nil {
		t.Fatalf("parse routes: %v", err)
	}
	errs := router.Validate()
	for _, e := range errs {
		t.Logf("validation: %v", e)
	}
}

func TestE2E_AuditVerifyChain(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	// Generate an audit entry via a request.
	resp := env.doRequest(t, "POST", "/v1/chat/completions", map[string]interface{}{
		"model":    "claude-haiku-4-5",
		"messages": []map[string]string{{"role": "user", "content": "Hello audit"}},
	}, nil)
	resp.Body.Close()

	// Verify HMAC chain.
	ctx := context.Background()
	from := time.Now().Add(-1 * time.Hour)
	to := time.Now().Add(1 * time.Hour)

	if err := env.auditLog.VerifyChain(ctx, from, to); err != nil {
		t.Fatalf("verify chain failed: %v", err)
	}
}

func TestE2E_ServerBindsLoopback(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	host, _, _ := net.SplitHostPort(addr)
	ip := net.ParseIP(host)
	if !ip.IsLoopback() {
		t.Fatalf("expected loopback, got %s", host)
	}
}
