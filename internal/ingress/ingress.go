package ingress

import (
	"context"
	"net/http"

	"github.com/helldriver666/prism/internal/types"
)

// Handler processes incoming HTTP requests.
// Extracts metadata and text parts for PII scanning.
// The original request body is preserved in RawBody for pass-through.
type Handler interface {
	// Handle validates the request, authenticates the token, applies rate limiting.
	// Determines ServiceType from URL path and/or request body.
	// Extracts TextParts for Privacy Pipeline.
	// Returns ParsedRequest for further processing.
	Handle(ctx context.Context, r *http.Request) (*types.ParsedRequest, error)
}

// RateLimiter limits request frequency per project.
type RateLimiter interface {
	// Allow returns true if the request is allowed within the limit.
	Allow(projectID string) bool
}
