package provider

import (
	"context"
	"fmt"

	"github.com/helldriver666/prism/internal/types"
)

// GeminiConfig holds configuration for the Gemini provider.
type GeminiConfig struct {
	BaseURL            string
	PriceInputPer1M    float64
	PriceOutputPer1M   float64
	PriceEmbeddingPer1M float64
}

func (c *GeminiConfig) defaults() {
	if c.BaseURL == "" {
		c.BaseURL = "https://generativelanguage.googleapis.com"
	}
	if c.PriceInputPer1M == 0 {
		c.PriceInputPer1M = 0.075
	}
	if c.PriceOutputPer1M == 0 {
		c.PriceOutputPer1M = 0.30
	}
	if c.PriceEmbeddingPer1M == 0 {
		c.PriceEmbeddingPer1M = 0.01
	}
}

type geminiProvider struct {
	cfg GeminiConfig
}

// NewGeminiProvider creates a new Gemini provider (stub — Execute returns ErrNotImplemented).
func NewGeminiProvider(cfg GeminiConfig) Provider {
	cfg.defaults()
	return &geminiProvider{cfg: cfg}
}

func (p *geminiProvider) ID() types.ProviderID {
	return types.ProviderGemini
}

func (p *geminiProvider) SupportedServices() []types.ServiceType {
	return []types.ServiceType{
		types.ServiceChat,
		types.ServiceEmbedding,
	}
}

func (p *geminiProvider) Execute(_ context.Context, _ *types.SanitizedRequest) (*types.Response, error) {
	return nil, ErrNotImplemented
}

func (p *geminiProvider) EstimateCost(req *types.ParsedRequest) (*types.CostEstimate, error) {
	var totalChars int
	for _, part := range req.TextParts {
		totalChars += len(part.Content)
	}
	inputTokens := totalChars / 4
	if inputTokens < 10 {
		inputTokens = 10
	}

	switch req.ServiceType {
	case types.ServiceChat:
		outputTokens := req.MaxTokens
		if outputTokens == 0 {
			outputTokens = 256
		}
		cost := float64(inputTokens)*p.cfg.PriceInputPer1M/1_000_000 +
			float64(outputTokens)*p.cfg.PriceOutputPer1M/1_000_000
		return &types.CostEstimate{
			EstimatedUSD: cost,
			BillingType:  types.BillingPerToken,
			Breakdown:    fmt.Sprintf("~%d input + ~%d output tokens", inputTokens, outputTokens),
		}, nil

	case types.ServiceEmbedding:
		cost := float64(inputTokens) * p.cfg.PriceEmbeddingPer1M / 1_000_000
		return &types.CostEstimate{
			EstimatedUSD: cost,
			BillingType:  types.BillingPerToken,
			Breakdown:    fmt.Sprintf("~%d tokens × $%.4f/1M", inputTokens, p.cfg.PriceEmbeddingPer1M),
		}, nil

	default:
		return &types.CostEstimate{BillingType: types.BillingPerToken}, nil
	}
}

func (p *geminiProvider) HealthCheck(_ context.Context) error {
	return ErrNotImplemented
}
