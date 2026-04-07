package cache

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type ollamaEmbedder struct {
	client  *http.Client
	baseURL string
	model   string
}

// NewOllamaEmbedder creates an Embedder that generates vectors via the Ollama API.
func NewOllamaEmbedder(baseURL, model string) Embedder {
	return &ollamaEmbedder{
		client:  &http.Client{},
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
	}
}

type embedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

func (e *ollamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	body, _ := json.Marshal(embedRequest{Model: e.model, Input: text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embedder: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedder: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("embedder: status %d: %s", resp.StatusCode, b)
	}

	var er embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		return nil, fmt.Errorf("embedder: decode: %w", err)
	}
	if len(er.Embeddings) == 0 || len(er.Embeddings[0]) == 0 {
		return nil, fmt.Errorf("embedder: empty embedding")
	}
	return er.Embeddings[0], nil
}

func (e *ollamaEmbedder) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.baseURL+"/api/tags", nil)
	if err != nil {
		return err
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("embedder: health: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("embedder: health status %d", resp.StatusCode)
	}
	return nil
}
