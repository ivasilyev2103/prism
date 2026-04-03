package privacy

import (
	"context"
	"encoding/json"

	"github.com/helldriver666/prism/internal/types"
)

// Profile defines the obfuscation level.
type Profile string

const (
	ProfileStrict   Profile = "strict"
	ProfileModerate Profile = "moderate"
	ProfileOff      Profile = "off"
)

// Entity is a detected PII entity.
type Entity struct {
	Type  string  // "PERSON" | "EMAIL" | "PHONE" | "CREDIT_CARD" | ...
	Value string
	Score float64 // detector confidence: 0.0–1.0
	Start int     // position in text
	End   int
}

// SanitizeResult is the result of obfuscating a single request.
type SanitizeResult struct {
	SanitizedParts []types.TextPart
	SanitizedBody  json.RawMessage // RawBody with substituted text fields
	PrivacyScore   float64
	EntitiesFound  []Entity
	// RestoreFunc restores original data in a text response.
	// IMPORTANT: RestoreFunc holds the Map Table in a closure.
	// Call exactly once. After the call, the Map Table is zeroed.
	// For binary responses (image, audio, 3D), RestoreFunc is not applied.
	RestoreFunc func(response string) string
}

// Pipeline performs PII obfuscation and restoration.
// Works only with text parts of the request (TextParts).
// Binary data (images, audio, 3D) is not scanned.
type Pipeline interface {
	// Sanitize detects PII in text parts and replaces them with placeholders.
	// profile and customPatterns come from the project configuration.
	// Returns SanitizeResult with RestoreFunc for reverse transformation.
	// For ServiceType without text input (STT with audio), returns pass-through.
	Sanitize(ctx context.Context, parts []types.TextPart, rawBody json.RawMessage, profile Profile, customPatterns []Pattern) (*SanitizeResult, error)
}

// Detector discovers PII entities in text.
// Separated from Pipeline for testability and support of multiple implementations:
//   - PresidioDetector: full-featured (Python sidecar)
//   - RegexDetector: built-in Go detector (email, phone, CC, SSN, IBAN, IP)
//   - OllamaDetector: NER through a local LLM
//   - CompositeDetector: merges results from multiple detectors
type Detector interface {
	// Detect returns a list of found entities.
	Detect(ctx context.Context, text string, profile Profile) ([]Entity, error)

	// HealthCheck verifies detector availability.
	HealthCheck(ctx context.Context) error
}

// Pattern is a custom pattern for detecting sensitive data.
type Pattern struct {
	Name  string  // e.g., "INTERNAL_USER_ID"
	Regex string  // e.g., "USR-[0-9]{8}"
	Score float64
}
