package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/helldriver666/prism/internal/types"
)

// OllamaConfig holds configuration for the Ollama provider.
type OllamaConfig struct {
	BaseURL        string // default: "http://localhost:11434"
	DefaultModel   string // default: "llama3"
	TimeoutSeconds int    // default: 120
	HTTPClient     *http.Client
}

func (c *OllamaConfig) defaults() {
	if c.BaseURL == "" {
		c.BaseURL = "http://localhost:11434"
	}
	if c.DefaultModel == "" {
		c.DefaultModel = "llama3"
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = 120
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: time.Duration(c.TimeoutSeconds) * time.Second}
	}
}

type ollamaProvider struct {
	cfg OllamaConfig
}

// NewOllamaProvider creates a new Ollama (local) provider.
func NewOllamaProvider(cfg OllamaConfig) Provider {
	cfg.defaults()
	return &ollamaProvider{cfg: cfg}
}

func (p *ollamaProvider) ID() types.ProviderID {
	return types.ProviderOllama
}

func (p *ollamaProvider) SupportedServices() []types.ServiceType {
	return []types.ServiceType{
		types.ServiceChat,
		types.ServiceEmbedding,
		types.ServiceImage,
	}
}

func (p *ollamaProvider) Execute(ctx context.Context, req *types.SanitizedRequest) (*types.Response, error) {
	start := time.Now()

	// Pick the right Ollama endpoint based on ServiceType.
	endpoint := p.endpointForService(req.ServiceType)
	url := p.cfg.BaseURL + endpoint

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bodyReader(req.SanitizedBody))
	if err != nil {
		return nil, fmt.Errorf("provider/ollama: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.cfg.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("provider/ollama: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("provider/ollama: read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("provider/ollama: error %d: %s", resp.StatusCode, body)
	}

	// Parse usage from Ollama response.
	var ollamaResp ollamaResponse
	_ = json.Unmarshal(body, &ollamaResp)

	return &types.Response{
		ID:          req.ID,
		ServiceType: req.ServiceType,
		TextContent: ollamaResp.Message.Content,
		ContentType: "text/plain",
		RawBody:     json.RawMessage(body),
		Usage: types.UsageMetrics{
			InputTokens:  ollamaResp.PromptEvalCount,
			OutputTokens: ollamaResp.EvalCount,
		},
		Model:      ollamaResp.Model,
		ProviderID: types.ProviderOllama,
		LatencyMS:  time.Since(start).Milliseconds(),
	}, nil
}

func (p *ollamaProvider) EstimateCost(_ *types.ParsedRequest) (*types.CostEstimate, error) {
	// Ollama is local and free.
	return &types.CostEstimate{
		EstimatedUSD: 0,
		BillingType:  types.BillingPerToken,
		Breakdown:    "local provider, no cost",
	}, nil
}

func (p *ollamaProvider) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.BaseURL+"/api/tags", nil)
	if err != nil {
		return err
	}
	resp, err := p.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("provider/ollama: health check failed: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("provider/ollama: health check returned %d", resp.StatusCode)
	}
	return nil
}

func (p *ollamaProvider) endpointForService(st types.ServiceType) string {
	switch st {
	case types.ServiceChat:
		return "/api/chat"
	case types.ServiceEmbedding:
		return "/api/embed"
	case types.ServiceImage:
		return "/api/generate"
	default:
		return "/api/chat"
	}
}

// --- Ollama API response types (internal) ---

type ollamaResponse struct {
	Model           string        `json:"model"`
	Message         ollamaMessage `json:"message"`
	PromptEvalCount int           `json:"prompt_eval_count"`
	EvalCount       int           `json:"eval_count"`
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
