package privacy

import (
	"strings"
	"sync"
)

// buildRestoreFunc creates a closure that restores original PII values in a text response.
//
// Security invariants:
//   - The Map Table is captured inside the closure and never exposed.
//   - First call: performs restoration and destroys the Map Table.
//   - Subsequent calls: return input unchanged (Map Table already zeroed).
//   - Placeholder whitelist: only placeholders with matching reqID are restored.
func buildRestoreFunc(mt map[string]string, reqID string) func(string) string {
	if len(mt) == 0 {
		return func(s string) string { return s }
	}

	var mu sync.Mutex
	destroyed := false

	return func(response string) string {
		mu.Lock()
		defer mu.Unlock()

		if destroyed {
			return response
		}

		result := applyRestore(response, mt, reqID)
		destroyMapTable(mt)
		destroyed = true
		return result
	}
}

// applyRestore replaces placeholders in text with original values.
// Only restores placeholders that contain the expected reqID prefix (whitelist).
func applyRestore(text string, mt map[string]string, reqID string) string {
	for placeholder, original := range mt {
		// Whitelist: only restore our own placeholders (contains our reqID).
		if !strings.Contains(placeholder, reqID) {
			continue
		}
		text = strings.ReplaceAll(text, placeholder, original)
	}
	return text
}
