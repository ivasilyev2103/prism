package privacy_test

import (
	"context"
	"encoding/json"

	"github.com/helldriver666/prism/internal/privacy"
	"github.com/helldriver666/prism/internal/types"
)

// Compile-time checks.
var (
	_ privacy.Pipeline = (*mockPipeline)(nil)
	_ privacy.Detector = (*mockDetector)(nil)
)

type mockPipeline struct {
	sanitizeFn func(ctx context.Context, parts []types.TextPart, rawBody json.RawMessage, profile privacy.Profile, customPatterns []privacy.Pattern) (*privacy.SanitizeResult, error)
}

func (m *mockPipeline) Sanitize(ctx context.Context, parts []types.TextPart, rawBody json.RawMessage, profile privacy.Profile, customPatterns []privacy.Pattern) (*privacy.SanitizeResult, error) {
	if m.sanitizeFn != nil {
		return m.sanitizeFn(ctx, parts, rawBody, profile, customPatterns)
	}
	return &privacy.SanitizeResult{
		SanitizedParts: parts,
		SanitizedBody:  rawBody,
		PrivacyScore:   0,
		EntitiesFound:  nil,
		RestoreFunc:    func(response string) string { return response },
	}, nil
}

type mockDetector struct {
	detectFn     func(ctx context.Context, text string, profile privacy.Profile) ([]privacy.Entity, error)
	healthCheckFn func(ctx context.Context) error
}

func (m *mockDetector) Detect(ctx context.Context, text string, profile privacy.Profile) ([]privacy.Entity, error) {
	if m.detectFn != nil {
		return m.detectFn(ctx, text, profile)
	}
	return nil, nil
}

func (m *mockDetector) HealthCheck(ctx context.Context) error {
	if m.healthCheckFn != nil {
		return m.healthCheckFn(ctx)
	}
	return nil
}
