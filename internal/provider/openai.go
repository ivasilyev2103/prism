package provider

import (
	"context"
	"fmt"

	"github.com/helldriver666/prism/internal/types"
)

// OpenAIConfig holds configuration for the OpenAI provider.
type OpenAIConfig struct {
	BaseURL              string
	PriceChatInputPer1M  float64
	PriceChatOutputPer1M float64
	PricePerImage        float64
	PriceEmbeddingPer1M  float64
	PricePerModeration   float64
	PriceTTSPerSecond    float64
	PriceSTTPerSecond    float64
}

func (c *OpenAIConfig) defaults() {
	if c.BaseURL == "" {
		c.BaseURL = "https://api.openai.com"
	}
	if c.PriceChatInputPer1M == 0 {
		c.PriceChatInputPer1M = 0.15
	}
	if c.PriceChatOutputPer1M == 0 {
		c.PriceChatOutputPer1M = 0.60
	}
	if c.PricePerImage == 0 {
		c.PricePerImage = 0.04
	}
	if c.PriceEmbeddingPer1M == 0 {
		c.PriceEmbeddingPer1M = 0.02
	}
	if c.PricePerModeration == 0 {
		c.PricePerModeration = 0
	}
	if c.PriceTTSPerSecond == 0 {
		c.PriceTTSPerSecond = 0.015
	}
	if c.PriceSTTPerSecond == 0 {
		c.PriceSTTPerSecond = 0.006
	}
}

type openaiProvider struct {
	cfg OpenAIConfig
}

// NewOpenAIProvider creates a new OpenAI provider (stub — Execute returns ErrNotImplemented).
func NewOpenAIProvider(cfg OpenAIConfig) Provider {
	cfg.defaults()
	return &openaiProvider{cfg: cfg}
}

func (p *openaiProvider) ID() types.ProviderID {
	return types.ProviderOpenAI
}

func (p *openaiProvider) SupportedServices() []types.ServiceType {
	return []types.ServiceType{
		types.ServiceChat,
		types.ServiceImage,
		types.ServiceEmbedding,
		types.ServiceTTS,
		types.ServiceSTT,
		types.ServiceModeration,
	}
}

func (p *openaiProvider) Execute(_ context.Context, _ *types.SanitizedRequest) (*types.Response, error) {
	return nil, ErrNotImplemented
}

func (p *openaiProvider) EstimateCost(req *types.ParsedRequest) (*types.CostEstimate, error) {
	switch req.ServiceType {
	case types.ServiceChat:
		var totalChars int
		for _, part := range req.TextParts {
			totalChars += len(part.Content)
		}
		inputTokens := totalChars / 4
		if inputTokens < 10 {
			inputTokens = 10
		}
		outputTokens := req.MaxTokens
		if outputTokens == 0 {
			outputTokens = 256
		}
		cost := float64(inputTokens)*p.cfg.PriceChatInputPer1M/1_000_000 +
			float64(outputTokens)*p.cfg.PriceChatOutputPer1M/1_000_000
		return &types.CostEstimate{
			EstimatedUSD: cost,
			BillingType:  types.BillingPerToken,
			Breakdown:    fmt.Sprintf("~%d input + ~%d output tokens", inputTokens, outputTokens),
		}, nil

	case types.ServiceImage:
		return &types.CostEstimate{
			EstimatedUSD: p.cfg.PricePerImage,
			BillingType:  types.BillingPerImage,
			Breakdown:    fmt.Sprintf("1 image × $%.4f", p.cfg.PricePerImage),
		}, nil

	case types.ServiceEmbedding:
		var totalChars int
		for _, part := range req.TextParts {
			totalChars += len(part.Content)
		}
		tokens := totalChars / 4
		if tokens < 10 {
			tokens = 10
		}
		cost := float64(tokens) * p.cfg.PriceEmbeddingPer1M / 1_000_000
		return &types.CostEstimate{
			EstimatedUSD: cost,
			BillingType:  types.BillingPerToken,
			Breakdown:    fmt.Sprintf("~%d tokens × $%.4f/1M", tokens, p.cfg.PriceEmbeddingPer1M),
		}, nil

	case types.ServiceModeration:
		return &types.CostEstimate{
			EstimatedUSD: p.cfg.PricePerModeration,
			BillingType:  types.BillingPerRequest,
			Breakdown:    "moderation request (free)",
		}, nil

	case types.ServiceTTS:
		return &types.CostEstimate{
			EstimatedUSD: p.cfg.PriceTTSPerSecond * 10,
			BillingType:  types.BillingPerSecond,
			Breakdown:    fmt.Sprintf("~10s × $%.4f/s", p.cfg.PriceTTSPerSecond),
		}, nil

	case types.ServiceSTT:
		return &types.CostEstimate{
			EstimatedUSD: p.cfg.PriceSTTPerSecond * 30,
			BillingType:  types.BillingPerSecond,
			Breakdown:    fmt.Sprintf("~30s × $%.4f/s", p.cfg.PriceSTTPerSecond),
		}, nil

	default:
		return &types.CostEstimate{BillingType: types.BillingPerRequest}, nil
	}
}

func (p *openaiProvider) HealthCheck(_ context.Context) error {
	return ErrNotImplemented
}
