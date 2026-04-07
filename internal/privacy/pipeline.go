package privacy

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/helldriver666/prism/internal/types"
)

type pipelineImpl struct {
	detector Detector
}

// NewPipeline creates a Privacy Pipeline that orchestrates PII detection,
// scoring, substitution, and restoration.
func NewPipeline(detector Detector) Pipeline {
	return &pipelineImpl{detector: detector}
}

func (p *pipelineImpl) Sanitize(
	ctx context.Context,
	parts []types.TextPart,
	rawBody json.RawMessage,
	profile Profile,
	customPatterns []Pattern,
) (*SanitizeResult, error) {
	// 1. ProfileOff → pass-through (no-op).
	if profile == ProfileOff {
		return passThrough(parts, rawBody), nil
	}

	// 2. No text to scan → pass-through.
	if len(parts) == 0 {
		return passThrough(parts, rawBody), nil
	}

	// 3. Detect PII in each TextPart.
	allEntities, totalTextLen, err := p.detectAll(ctx, parts, profile, customPatterns)
	if err != nil {
		return nil, fmt.Errorf("privacy pipeline: detection failed: %w", err)
	}

	// 4. No entities found → pass-through with score 0.
	if len(allEntities) == 0 {
		return passThrough(parts, rawBody), nil
	}

	// 5. Substitute PII → placeholders.
	sanitizedParts, sanitizedBody, mt, reqID := buildSubstitution(allEntities, parts, rawBody)

	// 6. Calculate privacy risk score.
	score := calculateScore(allEntities, totalTextLen)

	// 7. Build RestoreFunc (closure over Map Table).
	restoreFunc := buildRestoreFunc(mt, reqID)

	return &SanitizeResult{
		SanitizedParts: sanitizedParts,
		SanitizedBody:  sanitizedBody,
		PrivacyScore:   score,
		EntitiesFound:  allEntities,
		RestoreFunc:    restoreFunc,
	}, nil
}

// detectAll runs the composite detector on each TextPart and applies custom patterns.
// Returns merged entities (positions relative to each part's text) flattened into
// a single list using the original values (for substitution by value, not position).
func (p *pipelineImpl) detectAll(
	ctx context.Context,
	parts []types.TextPart,
	profile Profile,
	customPatterns []Pattern,
) ([]Entity, int, error) {
	totalTextLen := 0
	seen := make(map[string]Entity) // dedup by value, keep highest score

	for _, part := range parts {
		if part.Content == "" {
			continue
		}
		totalTextLen += len(part.Content)

		// Run the main detector.
		entities, err := p.detector.Detect(ctx, part.Content, profile)
		if err != nil {
			return nil, 0, err
		}

		// Apply custom patterns on top.
		entities = applyCustomPatterns(part.Content, customPatterns, entities)

		// Merge: keep highest-scored entity per unique value.
		for _, e := range entities {
			if existing, ok := seen[e.Value]; !ok || e.Score > existing.Score {
				seen[e.Value] = e
			}
		}
	}

	result := make([]Entity, 0, len(seen))
	for _, e := range seen {
		result = append(result, e)
	}
	return result, totalTextLen, nil
}

// applyCustomPatterns runs user-defined regex patterns and appends matches.
func applyCustomPatterns(text string, patterns []Pattern, existing []Entity) []Entity {
	for _, cp := range patterns {
		re, err := regexp.Compile(cp.Regex)
		if err != nil {
			continue // Skip invalid patterns.
		}
		locs := re.FindAllStringIndex(text, -1)
		for _, loc := range locs {
			existing = append(existing, Entity{
				Type:  cp.Name,
				Value: text[loc[0]:loc[1]],
				Score: cp.Score,
				Start: loc[0],
				End:   loc[1],
			})
		}
	}
	return existing
}

// passThrough returns a SanitizeResult that leaves everything unchanged.
func passThrough(parts []types.TextPart, rawBody json.RawMessage) *SanitizeResult {
	return &SanitizeResult{
		SanitizedParts: parts,
		SanitizedBody:  rawBody,
		PrivacyScore:   0,
		EntitiesFound:  nil,
		RestoreFunc:    func(s string) string { return s },
	}
}

// --- Context helpers for provider-aware error handling ---

type providerContextKey struct{}

// WithProviderID adds a provider ID to the context.
// Used by the policy engine so the privacy pipeline (or its caller)
// can distinguish cloud vs. local providers for fail-closed behaviour.
func WithProviderID(ctx context.Context, id types.ProviderID) context.Context {
	return context.WithValue(ctx, providerContextKey{}, id)
}

// ProviderIDFromContext extracts the provider ID from context.
func ProviderIDFromContext(ctx context.Context) (types.ProviderID, bool) {
	id, ok := ctx.Value(providerContextKey{}).(types.ProviderID)
	return id, ok
}

// IsLocalProvider returns true for providers where data stays on the machine.
func IsLocalProvider(id types.ProviderID) bool {
	return id == types.ProviderOllama
}

// HandleDetectorError decides whether a detector failure should block the request.
//
//   - Cloud providers (Claude, OpenAI, Gemini): fail-closed — data must not leave
//     the machine without PII sanitisation.
//   - Local providers (Ollama): pass-through — data never leaves the machine,
//     so PII detection is QoS, not a security requirement.
func HandleDetectorError(err error, providerID types.ProviderID) error {
	if err == nil {
		return nil
	}
	if IsLocalProvider(providerID) {
		return nil
	}
	return fmt.Errorf("PII detector unavailable, blocking request to cloud provider %s: %w", providerID, err)
}
