package provider_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/helldriver666/prism/internal/provider"
	"github.com/helldriver666/prism/internal/types"
)

// --- Registry ---

func TestRegistry_GetForService(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(provider.NewClaudeProvider(provider.ClaudeConfig{}))
	reg.Register(provider.NewOllamaProvider(provider.OllamaConfig{}))
	reg.Register(provider.NewOpenAIProvider(provider.OpenAIConfig{}))
	reg.Register(provider.NewGeminiProvider(provider.GeminiConfig{}))

	chat := reg.GetForService(types.ServiceChat)
	if len(chat) != 4 {
		t.Fatalf("expected 4 chat providers, got %d", len(chat))
	}

	image := reg.GetForService(types.ServiceImage)
	if len(image) != 2 {
		t.Fatalf("expected 2 image providers (openai, ollama), got %d", len(image))
	}

	tts := reg.GetForService(types.ServiceTTS)
	if len(tts) != 1 {
		t.Fatalf("expected 1 TTS provider (openai), got %d", len(tts))
	}

	embedding := reg.GetForService(types.ServiceEmbedding)
	if len(embedding) != 3 {
		t.Fatalf("expected 3 embedding providers (openai, ollama, gemini), got %d", len(embedding))
	}
}

func TestRegistry_Get(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(provider.NewClaudeProvider(provider.ClaudeConfig{}))

	p, err := reg.Get(types.ProviderClaude)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.ID() != types.ProviderClaude {
		t.Fatalf("expected claude, got %s", p.ID())
	}
}

func TestRegistry_GetUnknownProvider_ReturnsError(t *testing.T) {
	reg := provider.NewRegistry()
	_, err := reg.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestRegistry_RegisterDuplicate_Panics(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(provider.NewClaudeProvider(provider.ClaudeConfig{}))

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	reg.Register(provider.NewClaudeProvider(provider.ClaudeConfig{}))
}

func TestRegistry_All(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(provider.NewClaudeProvider(provider.ClaudeConfig{}))
	reg.Register(provider.NewOllamaProvider(provider.OllamaConfig{}))

	all := reg.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(all))
	}
}

// --- Claude Provider ---

func TestClaudeProvider_Execute_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":    "msg_123",
			"model": "claude-haiku-4-5",
			"content": []map[string]string{
				{"type": "text", "text": "Hello from Claude!"},
			},
			"usage": map[string]int{
				"input_tokens":  50,
				"output_tokens": 12,
			},
		})
	}))
	defer server.Close()

	p := provider.NewClaudeProvider(provider.ClaudeConfig{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})

	req := &types.SanitizedRequest{
		ParsedRequest: types.ParsedRequest{
			ID:          "req_1",
			ServiceType: types.ServiceChat,
			Model:       "claude-haiku-4-5",
			RawBody:     json.RawMessage(`{"messages":[{"role":"user","content":"Hi"}]}`),
		},
		SanitizedBody: json.RawMessage(`{"messages":[{"role":"user","content":"Hi"}]}`),
	}

	resp, err := p.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if resp.TextContent != "Hello from Claude!" {
		t.Fatalf("expected 'Hello from Claude!', got %q", resp.TextContent)
	}
	if resp.Usage.InputTokens != 50 {
		t.Fatalf("expected 50 input tokens, got %d", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 12 {
		t.Fatalf("expected 12 output tokens, got %d", resp.Usage.OutputTokens)
	}
	if resp.ProviderID != types.ProviderClaude {
		t.Fatalf("expected provider claude, got %s", resp.ProviderID)
	}
	if resp.LatencyMS <= 0 {
		t.Fatal("expected positive latency")
	}
}

func TestClaudeProvider_Execute_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(200)
	}))
	defer server.Close()

	p := provider.NewClaudeProvider(provider.ClaudeConfig{
		BaseURL:        server.URL,
		TimeoutSeconds: 1,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	req := &types.SanitizedRequest{
		ParsedRequest: types.ParsedRequest{
			ID:          "req_timeout",
			ServiceType: types.ServiceChat,
			RawBody:     json.RawMessage(`{}`),
		},
		SanitizedBody: json.RawMessage(`{}`),
	}

	_, err := p.Execute(ctx, req)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestClaudeProvider_Execute_5xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, `{"error":"internal server error"}`)
	}))
	defer server.Close()

	p := provider.NewClaudeProvider(provider.ClaudeConfig{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})

	req := &types.SanitizedRequest{
		ParsedRequest: types.ParsedRequest{
			ID:          "req_5xx",
			ServiceType: types.ServiceChat,
			RawBody:     json.RawMessage(`{}`),
		},
		SanitizedBody: json.RawMessage(`{}`),
	}

	_, err := p.Execute(context.Background(), req)
	if err == nil {
		t.Fatal("expected error on 5xx response")
	}
}

func TestClaudeProvider_EstimateCost(t *testing.T) {
	p := provider.NewClaudeProvider(provider.ClaudeConfig{
		PriceInputPer1M:  1.0,
		PriceOutputPer1M: 5.0,
	})

	req := &types.ParsedRequest{
		ServiceType: types.ServiceChat,
		TextParts: []types.TextPart{
			{Content: "Hello, how are you doing today?"},
		},
		MaxTokens: 100,
	}

	est, err := p.EstimateCost(req)
	if err != nil {
		t.Fatalf("EstimateCost: %v", err)
	}

	if est.BillingType != types.BillingPerToken {
		t.Fatalf("expected per_token billing, got %s", est.BillingType)
	}
	if est.EstimatedUSD <= 0 {
		t.Fatal("expected positive cost estimate")
	}
	if est.Breakdown == "" {
		t.Fatal("expected non-empty breakdown")
	}
}

func TestClaudeProvider_SupportedServices(t *testing.T) {
	p := provider.NewClaudeProvider(provider.ClaudeConfig{})
	services := p.SupportedServices()

	if len(services) != 1 || services[0] != types.ServiceChat {
		t.Fatalf("expected [chat], got %v", services)
	}
}

// --- Ollama Provider ---

func TestOllamaProvider_SupportedServices(t *testing.T) {
	p := provider.NewOllamaProvider(provider.OllamaConfig{})
	services := p.SupportedServices()

	if len(services) != 3 {
		t.Fatalf("expected 3 services, got %d", len(services))
	}

	expected := map[types.ServiceType]bool{
		types.ServiceChat:      true,
		types.ServiceEmbedding: true,
		types.ServiceImage:     true,
	}
	for _, s := range services {
		if !expected[s] {
			t.Fatalf("unexpected service: %s", s)
		}
	}
}

func TestOllamaProvider_Execute_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"model": "llama3",
			"message": map[string]string{
				"role":    "assistant",
				"content": "Hello from Ollama!",
			},
			"prompt_eval_count": 25,
			"eval_count":        10,
		})
	}))
	defer server.Close()

	p := provider.NewOllamaProvider(provider.OllamaConfig{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})

	req := &types.SanitizedRequest{
		ParsedRequest: types.ParsedRequest{
			ID:          "req_ollama",
			ServiceType: types.ServiceChat,
			RawBody:     json.RawMessage(`{}`),
		},
		SanitizedBody: json.RawMessage(`{}`),
	}

	resp, err := p.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp.TextContent != "Hello from Ollama!" {
		t.Fatalf("expected 'Hello from Ollama!', got %q", resp.TextContent)
	}
	if resp.ProviderID != types.ProviderOllama {
		t.Fatalf("expected ollama, got %s", resp.ProviderID)
	}
}

func TestOllamaProvider_HealthCheck_Unavailable(t *testing.T) {
	// Point to a port that nothing listens on.
	p := provider.NewOllamaProvider(provider.OllamaConfig{
		BaseURL:        "http://127.0.0.1:19999",
		TimeoutSeconds: 1,
	})

	err := p.HealthCheck(context.Background())
	if err == nil {
		t.Fatal("expected error for unavailable Ollama")
	}
}

func TestOllamaProvider_EstimateCost_AlwaysZero(t *testing.T) {
	p := provider.NewOllamaProvider(provider.OllamaConfig{})
	est, err := p.EstimateCost(&types.ParsedRequest{ServiceType: types.ServiceChat})
	if err != nil {
		t.Fatalf("EstimateCost: %v", err)
	}
	if est.EstimatedUSD != 0 {
		t.Fatalf("expected 0 cost for Ollama, got %f", est.EstimatedUSD)
	}
}

func TestOllamaProvider_Execute_EmbeddingEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("expected /api/embed, got %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"model": "nomic-embed-text",
		})
	}))
	defer server.Close()

	p := provider.NewOllamaProvider(provider.OllamaConfig{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})

	req := &types.SanitizedRequest{
		ParsedRequest: types.ParsedRequest{
			ID:          "req_embed",
			ServiceType: types.ServiceEmbedding,
			RawBody:     json.RawMessage(`{}`),
		},
		SanitizedBody: json.RawMessage(`{}`),
	}

	_, err := p.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
}

// --- OpenAI Provider (stub) ---

func TestOpenAIProvider_SupportedServices(t *testing.T) {
	p := provider.NewOpenAIProvider(provider.OpenAIConfig{})
	services := p.SupportedServices()

	expected := map[types.ServiceType]bool{
		types.ServiceChat:       true,
		types.ServiceImage:      true,
		types.ServiceEmbedding:  true,
		types.ServiceTTS:        true,
		types.ServiceSTT:        true,
		types.ServiceModeration: true,
	}

	if len(services) != len(expected) {
		t.Fatalf("expected %d services, got %d", len(expected), len(services))
	}
	for _, s := range services {
		if !expected[s] {
			t.Fatalf("unexpected service: %s", s)
		}
	}
}

func TestOpenAIProvider_Execute_ReturnsNotImplemented(t *testing.T) {
	p := provider.NewOpenAIProvider(provider.OpenAIConfig{})
	_, err := p.Execute(context.Background(), &types.SanitizedRequest{})
	if err != provider.ErrNotImplemented {
		t.Fatalf("expected ErrNotImplemented, got %v", err)
	}
}

func TestOpenAIProvider_EstimateCost_PerBillingType(t *testing.T) {
	p := provider.NewOpenAIProvider(provider.OpenAIConfig{})

	tests := []struct {
		serviceType types.ServiceType
		billing     types.BillingType
	}{
		{types.ServiceChat, types.BillingPerToken},
		{types.ServiceImage, types.BillingPerImage},
		{types.ServiceEmbedding, types.BillingPerToken},
		{types.ServiceModeration, types.BillingPerRequest},
		{types.ServiceTTS, types.BillingPerSecond},
		{types.ServiceSTT, types.BillingPerSecond},
	}

	for _, tt := range tests {
		t.Run(string(tt.serviceType), func(t *testing.T) {
			req := &types.ParsedRequest{
				ServiceType: tt.serviceType,
				TextParts:   []types.TextPart{{Content: "test input"}},
			}
			est, err := p.EstimateCost(req)
			if err != nil {
				t.Fatalf("EstimateCost: %v", err)
			}
			if est.BillingType != tt.billing {
				t.Fatalf("expected %s, got %s", tt.billing, est.BillingType)
			}
		})
	}
}

// --- Gemini Provider (stub) ---

func TestGeminiProvider_SupportedServices(t *testing.T) {
	p := provider.NewGeminiProvider(provider.GeminiConfig{})
	services := p.SupportedServices()

	if len(services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(services))
	}
}

func TestGeminiProvider_Execute_ReturnsNotImplemented(t *testing.T) {
	p := provider.NewGeminiProvider(provider.GeminiConfig{})
	_, err := p.Execute(context.Background(), &types.SanitizedRequest{})
	if err != provider.ErrNotImplemented {
		t.Fatalf("expected ErrNotImplemented, got %v", err)
	}
}

func TestGeminiProvider_EstimateCost_Chat(t *testing.T) {
	p := provider.NewGeminiProvider(provider.GeminiConfig{})
	req := &types.ParsedRequest{
		ServiceType: types.ServiceChat,
		TextParts:   []types.TextPart{{Content: "Hello world"}},
	}
	est, err := p.EstimateCost(req)
	if err != nil {
		t.Fatalf("EstimateCost: %v", err)
	}
	if est.BillingType != types.BillingPerToken {
		t.Fatalf("expected per_token, got %s", est.BillingType)
	}
	if est.EstimatedUSD <= 0 {
		t.Fatal("expected positive cost estimate")
	}
}

func TestGeminiProvider_EstimateCost_Embedding(t *testing.T) {
	p := provider.NewGeminiProvider(provider.GeminiConfig{})
	req := &types.ParsedRequest{
		ServiceType: types.ServiceEmbedding,
		TextParts:   []types.TextPart{{Content: "embed this text"}},
	}
	est, err := p.EstimateCost(req)
	if err != nil {
		t.Fatalf("EstimateCost: %v", err)
	}
	if est.BillingType != types.BillingPerToken {
		t.Fatalf("expected per_token, got %s", est.BillingType)
	}
}
