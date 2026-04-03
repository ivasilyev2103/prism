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

// ClaudeConfig holds configuration for the Claude provider.
type ClaudeConfig struct {
	BaseURL            string  // default: "https://api.anthropic.com"
	PriceInputPer1M    float64 // default: 0.25 (Haiku)
	PriceOutputPer1M   float64 // default: 1.25 (Haiku)
	DefaultModel       string  // default: "claude-haiku-4-5"
	TimeoutSeconds     int     // default: 60
	HTTPClient         *http.Client
}

func (c *ClaudeConfig) defaults() {
	if c.BaseURL == "" {
		c.BaseURL = "https://api.anthropic.com"
	}
	if c.PriceInputPer1M == 0 {
		c.PriceInputPer1M = 0.25
	}
	if c.PriceOutputPer1M == 0 {
		c.PriceOutputPer1M = 1.25
	}
	if c.DefaultModel == "" {
		c.DefaultModel = "claude-haiku-4-5"
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = 60
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: time.Duration(c.TimeoutSeconds) * time.Second}
	}
}

type claudeProvider struct {
	cfg ClaudeConfig
}

// NewClaudeProvider creates a new Claude API provider.
func NewClaudeProvider(cfg ClaudeConfig) Provider {
	cfg.defaults()
	return &claudeProvider{cfg: cfg}
}

func (p *claudeProvider) ID() types.ProviderID {
	return types.ProviderClaude
}

func (p *claudeProvider) SupportedServices() []types.ServiceType {
	return []types.ServiceType{types.ServiceChat}
}

func (p *claudeProvider) Execute(ctx context.Context, req *types.SanitizedRequest) (*types.Response, error) {
	start := time.Now()

	// Build endpoint URL based on ServiceType.
	url := p.cfg.BaseURL + "/v1/messages"

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bodyReader(req.SanitizedBody))
	if err != nil {
		return nil, fmt.Errorf("provider/claude: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	// Auth header is injected by Vault's SignRequest/signingRoundTripper.
	// The HTTP client transport is set by Policy Engine.
	resp, err := p.cfg.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("provider/claude: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("provider/claude: read response: %w", err)
	}

	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("provider/claude: server error %d: %s", resp.StatusCode, body)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("provider/claude: client error %d: %s", resp.StatusCode, body)
	}

	// Parse the response to extract usage metrics.
	var claudeResp claudeResponse
	_ = json.Unmarshal(body, &claudeResp)

	var textContent string
	for _, block := range claudeResp.Content {
		if block.Type == "text" {
			textContent += block.Text
		}
	}

	return &types.Response{
		ID:          claudeResp.ID,
		ServiceType: types.ServiceChat,
		TextContent: textContent,
		ContentType: "text/plain",
		RawBody:     json.RawMessage(body),
		Usage: types.UsageMetrics{
			InputTokens:  claudeResp.Usage.InputTokens,
			OutputTokens: claudeResp.Usage.OutputTokens,
		},
		Model:      claudeResp.Model,
		ProviderID: types.ProviderClaude,
		LatencyMS:  time.Since(start).Milliseconds(),
	}, nil
}

func (p *claudeProvider) EstimateCost(req *types.ParsedRequest) (*types.CostEstimate, error) {
	// Rough token estimate: ~4 chars per token for text parts.
	var totalChars int
	for _, part := range req.TextParts {
		totalChars += len(part.Content)
	}
	estimatedInputTokens := totalChars / 4
	if estimatedInputTokens < 10 {
		estimatedInputTokens = 10
	}
	estimatedOutputTokens := req.MaxTokens
	if estimatedOutputTokens == 0 {
		estimatedOutputTokens = 256
	}

	inputCost := float64(estimatedInputTokens) * p.cfg.PriceInputPer1M / 1_000_000
	outputCost := float64(estimatedOutputTokens) * p.cfg.PriceOutputPer1M / 1_000_000
	total := inputCost + outputCost

	return &types.CostEstimate{
		EstimatedUSD: total,
		BillingType:  types.BillingPerToken,
		Breakdown: fmt.Sprintf("~%d input tokens × $%.2f/1M + ~%d output tokens × $%.2f/1M = $%.6f",
			estimatedInputTokens, p.cfg.PriceInputPer1M,
			estimatedOutputTokens, p.cfg.PriceOutputPer1M, total),
	}, nil
}

func (p *claudeProvider) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.BaseURL+"/v1/messages", nil)
	if err != nil {
		return err
	}
	resp, err := p.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("provider/claude: health check failed: %w", err)
	}
	resp.Body.Close()
	// Any response (even 401) means the API is reachable.
	return nil
}

// --- Claude API response types (internal) ---

type claudeResponse struct {
	ID      string         `json:"id"`
	Model   string         `json:"model"`
	Content []contentBlock `json:"content"`
	Usage   claudeUsage    `json:"usage"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type claudeUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// bodyReader wraps json.RawMessage as an io.Reader.
func bodyReader(body json.RawMessage) io.Reader {
	if body == nil {
		return nil
	}
	return io.NopCloser(readerFromBytes(body))
}

type bytesReader struct {
	data []byte
	pos  int
}

func readerFromBytes(b []byte) *bytesReader {
	return &bytesReader{data: b}
}

func (r *bytesReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}
