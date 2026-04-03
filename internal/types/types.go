package types

import (
	"encoding/json"
)

// ProviderID identifies an AI provider.
type ProviderID string

const (
	ProviderClaude ProviderID = "claude"
	ProviderGemini ProviderID = "gemini"
	ProviderOpenAI ProviderID = "openai"
	ProviderOllama ProviderID = "ollama"
)

// ServiceType defines the type of AI service.
// Prism is a universal AI gateway: not only LLM, but also image generation, TTS, 3D, etc.
type ServiceType string

const (
	ServiceChat       ServiceType = "chat"       // LLM chat completions
	ServiceImage      ServiceType = "image"      // text-to-image (DALL-E, Stable Diffusion)
	ServiceEmbedding  ServiceType = "embedding"  // text embeddings
	ServiceTTS        ServiceType = "tts"        // text-to-speech
	Service3DModel    ServiceType = "3d_model"   // text-to-3D model
	ServiceModeration ServiceType = "moderation" // content moderation
	ServiceSTT        ServiceType = "stt"        // speech-to-text
)

// BillingType describes the billing model.
type BillingType string

const (
	BillingPerToken     BillingType = "per_token"     // LLM: price per token
	BillingPerImage     BillingType = "per_image"     // image gen: price per image
	BillingPerRequest   BillingType = "per_request"   // fixed price per request
	BillingPerSecond    BillingType = "per_second"    // compute-time (3D, long TTS)
	BillingSubscription BillingType = "subscription"  // subscription with quotas
)

// TextPart is a text fragment extracted from the request for PII scanning.
// Prism uses pass-through architecture: it does not fully parse the request body,
// only extracts text fragments for the privacy pipeline.
type TextPart struct {
	Role    string // for chat: "system" | "user" | "assistant"; for others: "prompt" | "input"
	Content string
	Index   int // position in the original array (for restoration)
}

// ParsedRequest is a request after Ingress, before Privacy Pipeline.
// RawBody is preserved for pass-through to the provider (Prism does not impose its own format).
type ParsedRequest struct {
	ID          string
	ProjectID   string
	ServiceType ServiceType
	Model       string          // requested model (may be overridden by router)
	TextParts   []TextPart      // text parts for PII scanning
	Tags        []string
	RawBody     json.RawMessage // original request body (pass-through)
	MaxTokens   int             // for chat: response limit; 0 = not set
}

// SanitizedRequest is a request after Privacy Pipeline, ready to be sent to the provider.
type SanitizedRequest struct {
	ParsedRequest
	SanitizedBody    json.RawMessage // RawBody with substituted PII in text fields
	PrivacyScore     float64
	PIIEntitiesFound int
	// MapTable is intentionally absent — it is not passed between modules.
}

// Response is a provider response. A generalized type for all AI services.
type Response struct {
	ID          string
	ServiceType ServiceType
	TextContent string          // for text-based responses (chat, moderation)
	BinaryData  []byte          // for image/audio/3D responses
	ContentType string          // MIME: "text/plain", "image/png", "model/gltf+json", "audio/mp3"
	RawBody     json.RawMessage // original provider response (pass-through)
	Usage       UsageMetrics
	Model       string
	ProviderID  ProviderID
	LatencyMS   int64
}

// UsageMetrics contains resource consumption metrics (depend on service type).
type UsageMetrics struct {
	InputTokens  int     // chat, embedding
	OutputTokens int     // chat
	ImagesCount  int     // image generation
	AudioSeconds float64 // TTS, STT
	ComputeUnits float64 // 3D, generic compute
}

// CostEstimate is a preliminary cost estimate for a request.
type CostEstimate struct {
	EstimatedUSD float64
	BillingType  BillingType
	Breakdown    string // human-readable description: "~500 tokens x $0.003/1K = $0.0015"
}

// RequestRecord is a record for Cost Tracker and Audit Log.
type RequestRecord struct {
	ID               string
	Timestamp        int64
	ProjectID        string
	ProviderID       ProviderID
	ServiceType      ServiceType
	Model            string
	Usage            UsageMetrics
	CostUSD          float64
	BillingType      BillingType
	LatencyMS        int64
	PrivacyScore     float64
	PIIEntitiesFound int
	CacheHit         bool
	RouteMatched     string
	Status           string // "ok" | "error" | "budget_blocked" | "quota_exceeded" | "cache_hit"
}
