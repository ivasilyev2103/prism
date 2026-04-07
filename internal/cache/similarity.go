package cache

import "math"

// cosineSimilarity computes the cosine similarity between two vectors.
// Returns a value in [-1, 1]; identical vectors yield 1.0.
func cosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		ai, bi := float64(a[i]), float64(b[i])
		dot += ai * bi
		normA += ai * ai
		normB += bi * bi
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return float32(dot / denom)
}

// cosineDistance returns 1 - cosineSimilarity, in [0, 2].
func cosineDistance(a, b []float32) float32 {
	return 1 - cosineSimilarity(a, b)
}
