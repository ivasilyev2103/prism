package cache_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/helldriver666/prism/internal/cache"
	"github.com/helldriver666/prism/internal/types"
)

func newTestCache(t *testing.T, embedder cache.Embedder, policies []cache.CachePolicy) cache.SemanticCache {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "cache_test.db")
	key := []byte("test-encryption-key-32-bytes-ok!")
	c, err := cache.NewSemanticCache(dbPath, embedder, key, policies)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.(interface{ Close() error }).Close() })
	return c
}

func makeReq(svc types.ServiceType, project, text string) *types.SanitizedRequest {
	return &types.SanitizedRequest{
		ParsedRequest: types.ParsedRequest{
			ID: "req-1", ProjectID: project, ServiceType: svc, Model: "m",
			TextParts: []types.TextPart{{Role: "user", Content: text, Index: 0}},
			RawBody:   json.RawMessage(`{"text":"` + text + `"}`),
		},
		SanitizedBody: json.RawMessage(`{"text":"` + text + `"}`),
	}
}

func makeResp(text string) *types.Response {
	return &types.Response{
		TextContent: text,
		ServiceType: types.ServiceChat,
		ContentType: "text/plain",
	}
}

// --- Embedder that returns deterministic embeddings ---

type deterministicEmbedder struct {
	dim int
}

func (d *deterministicEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	// Generate a deterministic embedding from the text hash.
	emb := make([]float32, d.dim)
	h := uint32(0)
	for _, c := range text {
		h = h*31 + uint32(c)
	}
	for i := range emb {
		h = h*1103515245 + 12345
		emb[i] = float32(h%1000) / 1000.0
	}
	// Normalize.
	var norm float64
	for _, v := range emb {
		norm += float64(v) * float64(v)
	}
	norm = math.Sqrt(norm)
	for i := range emb {
		emb[i] = float32(float64(emb[i]) / norm)
	}
	return emb, nil
}

func (d *deterministicEmbedder) HealthCheck(context.Context) error { return nil }

// Embedder that returns near-identical embeddings for similar inputs.
type similarEmbedder struct {
	base []float32 // base embedding returned for all queries
}

func newSimilarEmbedder(dim int) *similarEmbedder {
	base := make([]float32, dim)
	for i := range base {
		base[i] = float32(i+1) / float32(dim)
	}
	return &similarEmbedder{base: base}
}

func (s *similarEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	// Return the same embedding → cosine similarity = 1.0
	cp := make([]float32, len(s.base))
	copy(cp, s.base)
	return cp, nil
}

func (s *similarEmbedder) HealthCheck(context.Context) error { return nil }

// --- Tests ---

func TestCache_HitAboveThreshold(t *testing.T) {
	// Same embedding for all texts → similarity = 1.0 → cache hit.
	emb := newSimilarEmbedder(64)
	c := newTestCache(t, emb, nil)

	req := makeReq(types.ServiceChat, "proj", "hello world")
	resp := makeResp("Hi there!")

	if err := c.Set(context.Background(), req, resp); err != nil {
		t.Fatal(err)
	}

	cached, err := c.Get(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if cached == nil {
		t.Fatal("expected cache hit")
	}
	if cached.TextContent != "Hi there!" {
		t.Errorf("expected 'Hi there!', got %q", cached.TextContent)
	}
}

func TestCache_MissBelowThreshold(t *testing.T) {
	// Different embeddings for different texts → low similarity → miss.
	emb := &deterministicEmbedder{dim: 64}
	c := newTestCache(t, emb, nil)

	req1 := makeReq(types.ServiceChat, "proj", "hello world")
	req2 := makeReq(types.ServiceChat, "proj", "completely different topic about quantum physics")

	c.Set(context.Background(), req1, makeResp("resp1"))

	cached, err := c.Get(context.Background(), req2)
	if err != nil {
		t.Fatal(err)
	}
	if cached != nil {
		t.Error("expected cache miss for dissimilar request")
	}
}

func TestCache_Invalidate_ClearsProject(t *testing.T) {
	emb := newSimilarEmbedder(64)
	c := newTestCache(t, emb, nil)

	req := makeReq(types.ServiceChat, "proj-a", "test query")
	c.Set(context.Background(), req, makeResp("response"))

	// Verify it's cached.
	cached, _ := c.Get(context.Background(), req)
	if cached == nil {
		t.Fatal("expected cache hit before invalidation")
	}

	// Invalidate.
	if err := c.Invalidate(context.Background(), "proj-a"); err != nil {
		t.Fatal(err)
	}

	// Should be gone.
	cached, _ = c.Get(context.Background(), req)
	if cached != nil {
		t.Error("expected cache miss after invalidation")
	}
}

func TestCache_PolicyDisabled_ForImageGen(t *testing.T) {
	emb := newSimilarEmbedder(64)
	c := newTestCache(t, emb, nil) // default policies: image disabled

	req := makeReq(types.ServiceImage, "proj", "a beautiful sunset")
	resp := makeResp("image data")

	// Set should be a no-op.
	c.Set(context.Background(), req, resp)

	// Get should return nil (disabled).
	cached, err := c.Get(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if cached != nil {
		t.Error("expected nil for disabled service type (image)")
	}
}

func TestCache_PolicyEnabled_ForChat(t *testing.T) {
	emb := newSimilarEmbedder(64)

	policies := []cache.CachePolicy{
		{ServiceType: types.ServiceChat, Enabled: true, TTL: 3600},
	}
	c := newTestCache(t, emb, policies)

	req := makeReq(types.ServiceChat, "proj", "test")
	c.Set(context.Background(), req, makeResp("ok"))

	cached, _ := c.Get(context.Background(), req)
	if cached == nil {
		t.Error("expected cache hit for enabled chat service")
	}
}

func TestCache_SanitizedResponse_EncryptedPIIMapping(t *testing.T) {
	emb := newSimilarEmbedder(64)
	c := newTestCache(t, emb, nil)

	req := makeReq(types.ServiceChat, "proj", "Contact [EMAIL_abc_1]")
	resp := makeResp("Response about [EMAIL_abc_1]")

	// Store sanitized response (with placeholders, not real PII).
	c.Set(context.Background(), req, resp)

	cached, _ := c.Get(context.Background(), req)
	if cached == nil {
		t.Fatal("expected cache hit")
	}
	// The cached response should contain placeholders, not real PII.
	if cached.TextContent != "Response about [EMAIL_abc_1]" {
		t.Errorf("expected sanitized response, got: %q", cached.TextContent)
	}
}

func TestEmbedder_HealthCheck_OllamaUnavailable(t *testing.T) {
	emb := &mockEmbedder{
		healthCheckFn: func(context.Context) error {
			return errors.New("connection refused")
		},
		embedFn: func(context.Context, string) ([]float32, error) {
			return nil, errors.New("connection refused")
		},
	}
	c := newTestCache(t, emb, nil)

	// When Ollama is unavailable, Get returns nil (miss), not error.
	req := makeReq(types.ServiceChat, "proj", "test")
	cached, err := c.Get(context.Background(), req)
	if err != nil {
		t.Errorf("expected nil error on Ollama failure, got: %v", err)
	}
	if cached != nil {
		t.Error("expected nil response on Ollama failure")
	}
}

func TestVectorIndex_Performance(t *testing.T) {
	// Insert many entries and verify search time is sublinear.
	emb := &deterministicEmbedder{dim: 64}
	c := newTestCache(t, emb, nil)

	n := 500
	for i := 0; i < n; i++ {
		req := makeReq(types.ServiceChat, "proj", repeatStr("text", i))
		c.Set(context.Background(), req, makeResp("r"))
	}

	// Search should complete quickly (VP-tree, not O(n) scan).
	start := time.Now()
	req := makeReq(types.ServiceChat, "proj", "search query")
	c.Get(context.Background(), req)
	elapsed := time.Since(start)

	// Sanity: should be under 1 second even on slow machines.
	if elapsed > time.Second {
		t.Errorf("search took too long: %v (expected O(log n))", elapsed)
	}
}

func TestCache_EmptyTextParts_Miss(t *testing.T) {
	emb := newSimilarEmbedder(64)
	c := newTestCache(t, emb, nil)

	req := &types.SanitizedRequest{
		ParsedRequest: types.ParsedRequest{
			ID: "r", ProjectID: "p", ServiceType: types.ServiceChat,
		},
	}
	cached, _ := c.Get(context.Background(), req)
	if cached != nil {
		t.Error("expected miss for empty text parts")
	}
}

func repeatStr(s string, n int) string {
	out := s
	for i := 0; i < n%20; i++ {
		out += s
	}
	return out + string(rune('a'+n%26))
}
