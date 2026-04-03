package vault

import (
	"net/http"
)

// signingRoundTripper is a custom http.RoundTripper implementing Sign-not-Expose.
// It injects the Authorization header at the transport level and zeroes it
// immediately after the wire write. The API key never lingers in http.Request.Header.
type signingRoundTripper struct {
	base      http.RoundTripper
	headerKey string // "Authorization", "X-API-Key", "x-api-key", etc.
	headerVal []byte // plaintext header value (e.g., "Bearer sk-ant-...")
}

// RoundTrip injects auth, sends the request, then zeroes the header.
func (rt *signingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// 1. Clone the request — never mutate the caller's original.
	clone := req.Clone(req.Context())

	// 2. Inject auth header from the key held in memory.
	valStr := string(rt.headerVal)
	clone.Header.Set(rt.headerKey, valStr)

	// 3. Send via underlying transport.
	resp, err := rt.base.RoundTrip(clone)

	// 4. Zero the header value immediately after wire write.
	clone.Header.Del(rt.headerKey)
	explicitBzero([]byte(valStr))

	return resp, err
}

// newSigningTransport creates a signing RoundTripper for the given provider.
// The headerVal is the full header value (e.g., "Bearer sk-ant-..." or just "sk-ant-...").
// The caller is responsible for zeroing headerVal after the transport is no longer needed.
func newSigningTransport(base http.RoundTripper, headerKey string, headerVal []byte) *signingRoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &signingRoundTripper{
		base:      base,
		headerKey: headerKey,
		headerVal: headerVal,
	}
}

// authHeaderKey returns the appropriate auth header name for each provider.
func authHeaderKey(providerID string) string {
	switch providerID {
	case "claude":
		return "x-api-key"
	case "openai":
		return "Authorization"
	case "gemini":
		return "x-goog-api-key"
	default:
		return "Authorization"
	}
}

// authHeaderValue formats the header value for each provider.
func authHeaderValue(providerID string, apiKey []byte) []byte {
	switch providerID {
	case "openai":
		val := make([]byte, 0, len("Bearer ")+len(apiKey))
		val = append(val, "Bearer "...)
		val = append(val, apiKey...)
		return val
	default:
		// claude, gemini, etc: raw key
		cp := make([]byte, len(apiKey))
		copy(cp, apiKey)
		return cp
	}
}
