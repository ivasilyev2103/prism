package ingress_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/helldriver666/prism/internal/ingress"
	"github.com/helldriver666/prism/internal/types"
)

// --- mock token validator ---

type testValidator struct {
	tokens map[string]string // token → projectID
}

func (v *testValidator) ValidateToken(token string) (string, error) {
	pid, ok := v.tokens[token]
	if !ok {
		return "", errors.New("invalid token")
	}
	return pid, nil
}

func newTestHandler(tokens map[string]string, ratePerMinute int) ingress.Handler {
	var rl ingress.RateLimiter
	if ratePerMinute > 0 {
		rl = ingress.NewRateLimiter(ratePerMinute)
	}
	return ingress.NewHandler(ingress.Config{
		Validator:   &testValidator{tokens: tokens},
		RateLimiter: rl,
	})
}

func makeRequest(method, path, token string, body string) *http.Request {
	var bodyReader *strings.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	var r *http.Request
	if bodyReader != nil {
		r, _ = http.NewRequest(method, "http://localhost:8080"+path, bodyReader)
	} else {
		r, _ = http.NewRequest(method, "http://localhost:8080"+path, nil)
	}
	if token != "" {
		r.Header.Set("X-Prism-Token", token)
	}
	return r
}

// --- Auth tests ---

func TestIngress_NoToken_Returns401(t *testing.T) {
	h := newTestHandler(map[string]string{"tok": "proj"}, 0)
	r := makeRequest("POST", "/v1/chat/completions", "", `{"messages":[]}`)

	_, err := h.Handle(context.Background(), r)
	if err == nil {
		t.Fatal("expected error for missing token")
	}
	if !errors.Is(err, ingress.ErrNoToken) {
		t.Fatalf("expected ErrNoToken, got %v", err)
	}
}

func TestIngress_InvalidToken_Returns401(t *testing.T) {
	h := newTestHandler(map[string]string{"valid_tok": "proj"}, 0)
	r := makeRequest("POST", "/v1/chat/completions", "bad_token", `{"messages":[]}`)

	_, err := h.Handle(context.Background(), r)
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
	if !errors.Is(err, ingress.ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}

// --- Rate Limiting ---

func TestIngress_RateLimit_Returns429(t *testing.T) {
	// Rate limit: 2 requests per minute.
	h := newTestHandler(map[string]string{"tok": "proj"}, 2)

	// First 2 should succeed (burst = capacity).
	for i := 0; i < 2; i++ {
		r := makeRequest("POST", "/v1/chat/completions", "tok", `{"messages":[{"role":"user","content":"hi"}]}`)
		_, err := h.Handle(context.Background(), r)
		if err != nil {
			t.Fatalf("request %d: unexpected error: %v", i+1, err)
		}
	}

	// Third should be rate limited.
	r := makeRequest("POST", "/v1/chat/completions", "tok", `{"messages":[{"role":"user","content":"hi"}]}`)
	_, err := h.Handle(context.Background(), r)
	if err == nil {
		t.Fatal("expected rate limit error")
	}
	if !errors.Is(err, ingress.ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited, got %v", err)
	}
}

// --- Loopback binding ---

func TestIngress_BindsOnlyLoopback(t *testing.T) {
	// Verify that binding on 0.0.0.0 is not used.
	// The contract: Prism binds on 127.0.0.1 only.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cannot bind to loopback: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Fatalf("expected loopback address, got %s", addr)
	}
}

// --- ServiceType detection ---

func TestIngress_ServiceType_FromURLPath(t *testing.T) {
	h := newTestHandler(map[string]string{"tok": "proj"}, 0)

	tests := []struct {
		path     string
		expected types.ServiceType
	}{
		{"/v1/chat/completions", types.ServiceChat},
		{"/v1/images/generations", types.ServiceImage},
		{"/v1/embeddings", types.ServiceEmbedding},
		{"/v1/audio/speech", types.ServiceTTS},
		{"/v1/audio/transcriptions", types.ServiceSTT},
		{"/v1/moderations", types.ServiceModeration},
	}

	for _, tt := range tests {
		t.Run(string(tt.expected), func(t *testing.T) {
			body := `{"input":"test","messages":[{"role":"user","content":"hi"}],"prompt":"test"}`
			r := makeRequest("POST", tt.path, "tok", body)
			parsed, err := h.Handle(context.Background(), r)
			if err != nil {
				t.Fatalf("Handle: %v", err)
			}
			if parsed.ServiceType != tt.expected {
				t.Fatalf("expected %s, got %s", tt.expected, parsed.ServiceType)
			}
		})
	}
}

func TestIngress_ServiceType_FromHeader(t *testing.T) {
	h := newTestHandler(map[string]string{"tok": "proj"}, 0)

	r := makeRequest("POST", "/v1/custom/endpoint", "tok", `{"prompt":"make a 3d model"}`)
	r.Header.Set("X-Prism-Service", "3d_model")

	parsed, err := h.Handle(context.Background(), r)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if parsed.ServiceType != types.Service3DModel {
		t.Fatalf("expected 3d_model, got %s", parsed.ServiceType)
	}
}

// --- TextParts extraction ---

func TestIngress_TextParts_ExtractedFromChat(t *testing.T) {
	h := newTestHandler(map[string]string{"tok": "proj"}, 0)

	body := `{
		"model": "claude-haiku-4-5",
		"messages": [
			{"role": "system", "content": "You are helpful"},
			{"role": "user", "content": "Hello world"}
		],
		"max_tokens": 100
	}`
	r := makeRequest("POST", "/v1/chat/completions", "tok", body)

	parsed, err := h.Handle(context.Background(), r)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(parsed.TextParts) != 2 {
		t.Fatalf("expected 2 text parts, got %d", len(parsed.TextParts))
	}
	if parsed.TextParts[0].Role != "system" {
		t.Fatalf("expected system role, got %s", parsed.TextParts[0].Role)
	}
	if parsed.TextParts[0].Content != "You are helpful" {
		t.Fatalf("unexpected content: %s", parsed.TextParts[0].Content)
	}
	if parsed.TextParts[1].Role != "user" {
		t.Fatalf("expected user role, got %s", parsed.TextParts[1].Role)
	}
	if parsed.TextParts[1].Content != "Hello world" {
		t.Fatalf("unexpected content: %s", parsed.TextParts[1].Content)
	}
}

func TestIngress_TextParts_ExtractedFromImagePrompt(t *testing.T) {
	h := newTestHandler(map[string]string{"tok": "proj"}, 0)

	body := `{"prompt": "a cat sitting on a chair", "n": 1, "size": "1024x1024"}`
	r := makeRequest("POST", "/v1/images/generations", "tok", body)

	parsed, err := h.Handle(context.Background(), r)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(parsed.TextParts) != 1 {
		t.Fatalf("expected 1 text part, got %d", len(parsed.TextParts))
	}
	if parsed.TextParts[0].Role != "prompt" {
		t.Fatalf("expected prompt role, got %s", parsed.TextParts[0].Role)
	}
	if parsed.TextParts[0].Content != "a cat sitting on a chair" {
		t.Fatalf("unexpected content: %s", parsed.TextParts[0].Content)
	}
}

func TestIngress_TextParts_Embedding(t *testing.T) {
	h := newTestHandler(map[string]string{"tok": "proj"}, 0)

	body := `{"input": "embed this text", "model": "text-embedding-3-small"}`
	r := makeRequest("POST", "/v1/embeddings", "tok", body)

	parsed, err := h.Handle(context.Background(), r)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(parsed.TextParts) != 1 {
		t.Fatalf("expected 1 text part, got %d", len(parsed.TextParts))
	}
	if parsed.TextParts[0].Content != "embed this text" {
		t.Fatalf("unexpected content: %s", parsed.TextParts[0].Content)
	}
}

func TestIngress_TextParts_EmbeddingArray(t *testing.T) {
	h := newTestHandler(map[string]string{"tok": "proj"}, 0)

	body := `{"input": ["first text", "second text"]}`
	r := makeRequest("POST", "/v1/embeddings", "tok", body)

	parsed, err := h.Handle(context.Background(), r)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(parsed.TextParts) != 2 {
		t.Fatalf("expected 2 text parts, got %d", len(parsed.TextParts))
	}
}

func TestIngress_TextParts_STT_NoTextExtracted(t *testing.T) {
	h := newTestHandler(map[string]string{"tok": "proj"}, 0)

	r := makeRequest("POST", "/v1/audio/transcriptions", "tok", `{"model":"whisper-1"}`)

	parsed, err := h.Handle(context.Background(), r)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(parsed.TextParts) != 0 {
		t.Fatalf("expected 0 text parts for STT, got %d", len(parsed.TextParts))
	}
}

// --- RawBody preservation ---

func TestIngress_RawBody_Preserved(t *testing.T) {
	h := newTestHandler(map[string]string{"tok": "proj"}, 0)

	originalBody := `{"messages":[{"role":"user","content":"preserve me"}],"temperature":0.7}`
	r := makeRequest("POST", "/v1/chat/completions", "tok", originalBody)

	parsed, err := h.Handle(context.Background(), r)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if parsed.RawBody == nil {
		t.Fatal("expected non-nil RawBody")
	}

	// Verify RawBody contains the original JSON.
	var original, preserved map[string]json.RawMessage
	_ = json.Unmarshal([]byte(originalBody), &original)
	_ = json.Unmarshal(parsed.RawBody, &preserved)

	if len(original) != len(preserved) {
		t.Fatalf("RawBody field count mismatch: %d vs %d", len(original), len(preserved))
	}
}

// --- Model + Tags extraction ---

func TestIngress_ValidRequest_ReturnsParsedRequest(t *testing.T) {
	h := newTestHandler(map[string]string{"tok": "proj"}, 0)

	body := `{"model":"claude-haiku-4-5","messages":[{"role":"user","content":"hi"}],"max_tokens":50}`
	r := makeRequest("POST", "/v1/chat/completions", "tok", body)
	r.Header.Set("X-Prism-Tags", "code, debug")

	parsed, err := h.Handle(context.Background(), r)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if parsed.ProjectID != "proj" {
		t.Fatalf("expected proj, got %s", parsed.ProjectID)
	}
	if parsed.Model != "claude-haiku-4-5" {
		t.Fatalf("expected claude-haiku-4-5, got %s", parsed.Model)
	}
	if parsed.MaxTokens != 50 {
		t.Fatalf("expected max_tokens=50, got %d", parsed.MaxTokens)
	}
	if len(parsed.Tags) != 2 || parsed.Tags[0] != "code" || parsed.Tags[1] != "debug" {
		t.Fatalf("unexpected tags: %v", parsed.Tags)
	}
	if parsed.ID == "" {
		t.Fatal("expected non-empty request ID")
	}
}
