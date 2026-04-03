package ingress

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/helldriver666/prism/internal/types"
)

// Config holds the configuration for creating a new ingress Handler.
type Config struct {
	Validator   tokenValidator // typically vault.Vault
	RateLimiter RateLimiter
}

// handler is the concrete implementation of Handler.
type handler struct {
	validator   tokenValidator
	rateLimiter RateLimiter
}

// NewHandler creates a new ingress handler.
func NewHandler(cfg Config) Handler {
	return &handler{
		validator:   cfg.Validator,
		rateLimiter: cfg.RateLimiter,
	}
}

// Handle validates the request, authenticates the token, applies rate limiting,
// determines ServiceType, extracts TextParts, and returns a ParsedRequest.
func (h *handler) Handle(ctx context.Context, r *http.Request) (*types.ParsedRequest, error) {
	// 1. Authenticate.
	projectID, err := authenticate(r, h.validator)
	if err != nil {
		return nil, err
	}

	// 2. Rate limit.
	if h.rateLimiter != nil && !h.rateLimiter.Allow(projectID) {
		return nil, ErrRateLimited
	}

	// 3. Determine ServiceType.
	serviceType, err := detectServiceType(r)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}

	// 4. Read and preserve original body.
	var rawBody json.RawMessage
	if r.Body != nil {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, fmt.Errorf("ingress: read body: %w", err)
		}
		if len(data) > 0 {
			rawBody = json.RawMessage(data)
		}
	}

	// 5. Extract TextParts for PII scanning.
	textParts := extractTextParts(serviceType, rawBody)

	// 6. Extract model and tags from body/headers.
	model := extractModel(rawBody)
	tags := extractTags(r)

	// 7. Extract max_tokens.
	maxTokens := extractMaxTokens(rawBody)

	return &types.ParsedRequest{
		ID:          generateRequestID(),
		ProjectID:   projectID,
		ServiceType: serviceType,
		Model:       model,
		TextParts:   textParts,
		Tags:        tags,
		RawBody:     rawBody,
		MaxTokens:   maxTokens,
	}, nil
}

// extractModel extracts the "model" field from the request body.
func extractModel(body json.RawMessage) string {
	if body == nil {
		return ""
	}
	var parsed struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	return parsed.Model
}

// extractTags extracts tags from the X-Prism-Tags header (comma-separated).
func extractTags(r *http.Request) []string {
	header := r.Header.Get("X-Prism-Tags")
	if header == "" {
		return nil
	}
	tags := strings.Split(header, ",")
	result := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t != "" {
			result = append(result, t)
		}
	}
	return result
}

// extractMaxTokens extracts max_tokens from the request body.
func extractMaxTokens(body json.RawMessage) int {
	if body == nil {
		return 0
	}
	var parsed struct {
		MaxTokens int `json:"max_tokens"`
	}
	_ = json.Unmarshal(body, &parsed)
	return parsed.MaxTokens
}

// generateRequestID creates a unique request ID.
func generateRequestID() string {
	return fmt.Sprintf("req_%d", time.Now().UnixNano())
}

