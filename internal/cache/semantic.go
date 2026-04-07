package cache

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/helldriver666/prism/internal/types"
)

const defaultThreshold float32 = 0.95

type semanticCache struct {
	store     *storage
	embedder  Embedder
	index     *vectorIndex
	policies  policyMap
	encKey    []byte
	threshold float32
}

// NewSemanticCache creates a SemanticCache backed by SQLite with a VP-tree index.
// encryptionKey is used for per-entry AES-256-GCM encryption of PII mappings.
// If embedder health check fails, the cache degrades gracefully (miss on Get, no-op on Set).
func NewSemanticCache(dbPath string, embedder Embedder, encryptionKey []byte, policies []CachePolicy) (SemanticCache, error) {
	store, err := newStorage(dbPath)
	if err != nil {
		return nil, err
	}

	idx := newVectorIndex()

	// Populate index from SQLite.
	entries, err := store.loadAll()
	if err != nil {
		store.close()
		return nil, fmt.Errorf("cache: load entries: %w", err)
	}
	for _, e := range entries {
		emb := decodeEmbedding(e.embedding)
		if len(emb) > 0 {
			idx.add(e.id, emb)
		}
	}

	if policies == nil {
		policies = DefaultPolicies()
	}

	return &semanticCache{
		store:     store,
		embedder:  embedder,
		index:     idx,
		policies:  buildPolicyMap(policies),
		encKey:    encryptionKey,
		threshold: defaultThreshold,
	}, nil
}

func (sc *semanticCache) Get(ctx context.Context, req *types.SanitizedRequest) (*types.Response, error) {
	if !sc.policies.isEnabled(req.ServiceType) {
		return nil, nil // caching disabled for this service type
	}

	// Build a text key from the sanitized request.
	text := requestText(req)
	if text == "" {
		return nil, nil
	}

	// Generate embedding.
	emb, err := sc.embedder.Embed(ctx, text)
	if err != nil {
		// Ollama unavailable — degrade to miss.
		return nil, nil
	}

	// Search VP-tree for similar entries.
	maxDist := 1 - sc.threshold
	candidates := sc.index.search(emb, maxDist)
	if len(candidates) == 0 {
		return nil, nil
	}

	// Find the best match.
	var bestID string
	var bestSim float32
	for _, c := range candidates {
		sim := cosineSimilarity(emb, c.embedding)
		if sim > bestSim {
			bestSim = sim
			bestID = c.id
		}
	}
	if bestID == "" {
		return nil, nil
	}

	// Load from storage.
	entry, err := sc.store.get(bestID)
	if err != nil {
		return nil, nil // entry evicted or corrupted
	}

	// Check TTL.
	if time.Now().Unix()-entry.createdAt > int64(entry.ttl) {
		return nil, nil // expired
	}

	// Deserialize response.
	var resp types.Response
	if err := json.Unmarshal(entry.response, &resp); err != nil {
		return nil, nil
	}

	return &resp, nil
}

func (sc *semanticCache) Set(ctx context.Context, req *types.SanitizedRequest, resp *types.Response) error {
	if !sc.policies.isEnabled(req.ServiceType) {
		return nil // caching disabled
	}

	text := requestText(req)
	if text == "" {
		return nil
	}

	emb, err := sc.embedder.Embed(ctx, text)
	if err != nil {
		return nil // Ollama unavailable — skip silently
	}

	entryID := generateEntryID(req)
	respBytes, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("cache: marshal response: %w", err)
	}

	// Encrypt PII mapping (placeholder if no PII).
	var encryptedMapping []byte
	// PII mapping is not passed through the interface — stored as empty for now.
	// In a full implementation, the SanitizeResult's map table would be passed here.

	err = sc.store.put(&storedEntry{
		id:          entryID,
		projectID:   req.ProjectID,
		serviceType: string(req.ServiceType),
		model:       req.Model,
		embedding:   encodeEmbedding(emb),
		response:    respBytes,
		piiMapping:  encryptedMapping,
		createdAt:   time.Now().Unix(),
		ttl:         sc.policies.ttl(req.ServiceType),
	})
	if err != nil {
		return fmt.Errorf("cache: store: %w", err)
	}

	sc.index.add(entryID, emb)
	return nil
}

func (sc *semanticCache) Invalidate(ctx context.Context, projectID string) error {
	if err := sc.store.deleteByProject(projectID); err != nil {
		return fmt.Errorf("cache: invalidate: %w", err)
	}
	sc.index.removeByProject(projectID, func(id string) string {
		entry, err := sc.store.get(id)
		if err != nil {
			return ""
		}
		return entry.projectID
	})
	return nil
}

// Close releases resources.
func (sc *semanticCache) Close() error {
	return sc.store.close()
}

// requestText extracts a text key from the sanitized request for embedding.
func requestText(req *types.SanitizedRequest) string {
	if len(req.ParsedRequest.TextParts) == 0 {
		return ""
	}
	// Concatenate text parts for embedding.
	var text string
	for _, p := range req.ParsedRequest.TextParts {
		if text != "" {
			text += "\n"
		}
		text += p.Content
	}
	return text
}

// generateEntryID produces a deterministic ID from the request.
func generateEntryID(req *types.SanitizedRequest) string {
	h := sha256.New()
	h.Write([]byte(req.ProjectID))
	h.Write([]byte(req.ServiceType))
	h.Write(req.SanitizedBody)
	sum := h.Sum(nil)
	return fmt.Sprintf("c_%x", sum[:12])
}

// encodeEmbedding serializes a float32 slice to bytes (little-endian).
func encodeEmbedding(emb []float32) []byte {
	buf := make([]byte, len(emb)*4)
	for i, v := range emb {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

// decodeEmbedding deserializes bytes to a float32 slice.
func decodeEmbedding(data []byte) []float32 {
	if len(data)%4 != 0 {
		return nil
	}
	emb := make([]float32, len(data)/4)
	for i := range emb {
		emb[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
	}
	return emb
}
