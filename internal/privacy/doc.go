// Package privacy implements PII detection and obfuscation for the Prism gateway.
//
// It provides a tiered detection architecture:
//   - Tier 1 (built-in): RegexDetector — email, phone, credit card, SSN, IBAN, IP (~70% coverage)
//   - Tier 2 (recommended): + OllamaDetector — names, organizations, addresses via local LLM (~95%)
//   - Tier 3 (maximum): + PresidioDetector — full NER via Python sidecar
//
// The Pipeline works only with text data (TextParts). Binary data (images, audio, 3D)
// is not scanned — PII in generated media is out of scope.
//
// Security invariants:
//   - Map Table (PII ↔ placeholder) lives only inside the RestoreFunc closure
//   - RestoreFunc must be called exactly once; after the call the Map Table is zeroed
//   - Placeholders contain a request-unique prefix to prevent prompt injection
//   - For cloud providers, detector failure means fail-closed (request blocked)
//   - For local providers (Ollama), detector failure means pass-through
package privacy
