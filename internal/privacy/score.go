package privacy

// calculateScore computes a privacy risk score based on detected entities.
// Formula: max(entity scores) * coverage_factor
// where coverage_factor = fraction of total text covered by PII entities.
// Returns a value between 0.0 (no PII) and 1.0 (high risk).
func calculateScore(entities []Entity, totalTextLength int) float64 {
	if len(entities) == 0 || totalTextLength == 0 {
		return 0.0
	}

	maxScore := 0.0
	coveredChars := 0
	for _, e := range entities {
		if e.Score > maxScore {
			maxScore = e.Score
		}
		coveredChars += e.End - e.Start
	}

	coverageFactor := float64(coveredChars) / float64(totalTextLength)
	if coverageFactor > 1.0 {
		coverageFactor = 1.0
	}

	return maxScore * coverageFactor
}
