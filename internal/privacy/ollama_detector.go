package privacy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type ollamaDetector struct {
	client  *http.Client
	baseURL string
	model   string
}

// NewOllamaDetector creates a Tier 2 NER detector using a local Ollama LLM.
// Detects: PERSON, ORGANIZATION, ADDRESS.
// Requires a running Ollama instance.
func NewOllamaDetector(baseURL, model string) Detector {
	return &ollamaDetector{
		client:  &http.Client{},
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
	}
}

// ollamaRequest is the request payload for Ollama /api/generate.
type ollamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
	Format string `json:"format"`
}

// ollamaResponse is the response from Ollama /api/generate.
type ollamaResponse struct {
	Response string `json:"response"`
}

// ollamaNEREntity is an entity parsed from the LLM's JSON response.
type ollamaNEREntity struct {
	Type  string `json:"type"`
	Value string `json:"value"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

const nerPrompt = `Extract all personally identifiable information (PII) from the text below.
Return ONLY a JSON array. Each element must have these exact keys:
- "type": one of PERSON, ORGANIZATION, ADDRESS
- "value": the exact substring from the text
- "start": 0-based character offset where the value starts
- "end": 0-based character offset where the value ends (exclusive)

If no PII is found, return an empty array: []

Text: %q

JSON:`

func (d *ollamaDetector) Detect(ctx context.Context, text string, profile Profile) ([]Entity, error) {
	if profile == ProfileOff {
		return nil, nil
	}

	prompt := fmt.Sprintf(nerPrompt, text)
	reqBody, err := json.Marshal(ollamaRequest{
		Model:  d.model,
		Prompt: prompt,
		Stream: false,
		Format: "json",
	})
	if err != nil {
		return nil, fmt.Errorf("ollama: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, d.baseURL+"/api/generate", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("ollama: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("ollama: status %d: %s", resp.StatusCode, body)
	}

	var ollamaResp ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, fmt.Errorf("ollama: decode response: %w", err)
	}

	return parseNERResponse(ollamaResp.Response, text)
}

func (d *ollamaDetector) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.baseURL+"/api/tags", nil)
	if err != nil {
		return fmt.Errorf("ollama: create health request: %w", err)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("ollama: health check failed: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama: health check status %d", resp.StatusCode)
	}
	return nil
}

// parseNERResponse extracts entities from the LLM's JSON response.
// Validates that reported positions match the actual text.
func parseNERResponse(response, originalText string) ([]Entity, error) {
	response = strings.TrimSpace(response)
	if response == "" || response == "[]" {
		return nil, nil
	}

	var nerEntities []ollamaNEREntity
	if err := json.Unmarshal([]byte(response), &nerEntities); err != nil {
		// LLM may produce malformed JSON — treat as no entities found.
		return nil, nil
	}

	var entities []Entity
	for _, ne := range nerEntities {
		if ne.Value == "" {
			continue
		}
		// Validate positions against original text.
		start := ne.Start
		end := ne.End
		if start < 0 || end > len(originalText) || start >= end {
			// Positions invalid — try to find the value in the text.
			idx := strings.Index(originalText, ne.Value)
			if idx < 0 {
				continue
			}
			start = idx
			end = idx + len(ne.Value)
		}
		// Verify the value matches the text at the reported positions.
		if end <= len(originalText) && originalText[start:end] != ne.Value {
			idx := strings.Index(originalText, ne.Value)
			if idx < 0 {
				continue
			}
			start = idx
			end = idx + len(ne.Value)
		}
		entities = append(entities, Entity{
			Type:  ne.Type,
			Value: ne.Value,
			Score: 0.85, // LLM NER confidence baseline
			Start: start,
			End:   end,
		})
	}
	return entities, nil
}
