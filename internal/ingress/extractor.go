package ingress

import (
	"encoding/json"

	"github.com/helldriver666/prism/internal/types"
)

// extractTextParts extracts text fragments from the request body based on ServiceType.
// These fragments are used by the Privacy Pipeline for PII scanning.
// The original body is preserved as RawBody for pass-through.
func extractTextParts(serviceType types.ServiceType, body json.RawMessage) []types.TextPart {
	switch serviceType {
	case types.ServiceChat:
		return extractChatParts(body)
	case types.ServiceImage, types.Service3DModel:
		return extractPromptField(body, "prompt")
	case types.ServiceEmbedding:
		return extractInputField(body)
	case types.ServiceTTS:
		return extractInputField(body)
	case types.ServiceModeration:
		return extractInputField(body)
	case types.ServiceSTT:
		// Audio input — no text to extract.
		return nil
	default:
		return nil
	}
}

// extractChatParts extracts text from chat messages[].content (string only).
func extractChatParts(body json.RawMessage) []types.TextPart {
	var parsed struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil
	}

	var parts []types.TextPart
	for i, msg := range parsed.Messages {
		// Content can be a string or an array of content blocks.
		var text string
		if err := json.Unmarshal(msg.Content, &text); err == nil {
			parts = append(parts, types.TextPart{
				Role:    msg.Role,
				Content: text,
				Index:   i,
			})
			continue
		}

		// Try array of content blocks (OpenAI / Claude multimodal format).
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(msg.Content, &blocks); err == nil {
			for _, block := range blocks {
				if block.Type == "text" {
					parts = append(parts, types.TextPart{
						Role:    msg.Role,
						Content: block.Text,
						Index:   i,
					})
				}
			}
		}
	}
	return parts
}

// extractPromptField extracts the "prompt" field from the body.
func extractPromptField(body json.RawMessage, field string) []types.TextPart {
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil
	}

	raw, ok := parsed[field]
	if !ok {
		return nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return nil
	}

	return []types.TextPart{{
		Role:    "prompt",
		Content: text,
		Index:   0,
	}}
}

// extractInputField extracts the "input" field (string or string array).
func extractInputField(body json.RawMessage) []types.TextPart {
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil
	}

	raw, ok := parsed["input"]
	if !ok {
		return nil
	}

	// Try as string.
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []types.TextPart{{
			Role:    "input",
			Content: text,
			Index:   0,
		}}
	}

	// Try as array of strings.
	var texts []string
	if err := json.Unmarshal(raw, &texts); err == nil {
		parts := make([]types.TextPart, len(texts))
		for i, t := range texts {
			parts[i] = types.TextPart{
				Role:    "input",
				Content: t,
				Index:   i,
			}
		}
		return parts
	}

	return nil
}
