package privacy

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"strings"

	"github.com/helldriver666/prism/internal/types"
)

// replacement pairs ordered for substitution (longest first).
type replacementPair struct {
	original    string
	placeholder string
}

// buildSubstitution creates sanitized TextParts, a sanitized RawBody, and a
// Map Table mapping each placeholder back to its original value.
// Entities must be already merged (non-overlapping).
// Returns the reqID prefix used in placeholders.
func buildSubstitution(
	entities []Entity,
	parts []types.TextPart,
	rawBody json.RawMessage,
) (sanitizedParts []types.TextPart, sanitizedBody json.RawMessage, mt map[string]string, reqID string) {
	reqID = generateReqID()

	if len(entities) == 0 {
		return parts, rawBody, nil, reqID
	}

	// Deduplicate entity values and assign placeholders.
	counters := make(map[string]int)              // entity type → counter
	valueToPlaceholder := make(map[string]string)  // original value → placeholder
	var pairs []replacementPair

	for _, e := range entities {
		if _, exists := valueToPlaceholder[e.Value]; exists {
			continue
		}
		counters[e.Type]++
		ph := fmt.Sprintf("[%s_%s_%d]", e.Type, reqID, counters[e.Type])
		valueToPlaceholder[e.Value] = ph
		pairs = append(pairs, replacementPair{original: e.Value, placeholder: ph})
	}

	// Sort by original value length descending so longer matches replace first
	// (prevents partial replacement when one value is a substring of another).
	sort.Slice(pairs, func(i, j int) bool {
		return len(pairs[i].original) > len(pairs[j].original)
	})

	// Build the Map Table (placeholder → original).
	mt = make(map[string]string, len(pairs))
	for _, p := range pairs {
		mt[p.placeholder] = p.original
	}

	// Replace PII in TextParts.
	sanitizedParts = make([]types.TextPart, len(parts))
	for i, p := range parts {
		content := p.Content
		for _, rp := range pairs {
			content = strings.ReplaceAll(content, rp.original, rp.placeholder)
		}
		sanitizedParts[i] = types.TextPart{
			Role:    p.Role,
			Content: content,
			Index:   p.Index,
		}
	}

	// Replace PII in RawBody (JSON — values are escaped).
	sanitizedBody = replaceInRawBody(rawBody, pairs)

	return sanitizedParts, sanitizedBody, mt, reqID
}

// replaceInRawBody substitutes PII in the raw JSON body.
// Handles JSON string escaping: the PII value and its placeholder are
// both JSON-escaped before replacement in the raw bytes.
func replaceInRawBody(rawBody json.RawMessage, pairs []replacementPair) json.RawMessage {
	if len(rawBody) == 0 {
		return rawBody
	}
	body := string(rawBody)
	for _, p := range pairs {
		escaped := jsonEscapeValue(p.original)
		escapedPH := jsonEscapeValue(p.placeholder)
		body = strings.ReplaceAll(body, escaped, escapedPH)
	}
	return json.RawMessage(body)
}

// jsonEscapeValue returns the JSON-escaped form of s (without surrounding quotes).
func jsonEscapeValue(s string) string {
	b, _ := json.Marshal(s)
	// json.Marshal returns `"escaped"` — strip the quotes.
	return string(b[1 : len(b)-1])
}

// generateReqID returns 8 random hex characters for placeholder isolation.
func generateReqID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// Fallback — should never happen with crypto/rand.
		return "00000000"
	}
	return hex.EncodeToString(b)
}

// destroyMapTable zeroes and clears the Map Table.
// Best-effort: Go strings are immutable and GC may copy data.
// Real security boundary is process isolation + mTLS.
func destroyMapTable(mt map[string]string) {
	for k := range mt {
		mt[k] = ""
	}
	for k := range mt {
		delete(mt, k)
	}
	runtime.KeepAlive(mt)
}
