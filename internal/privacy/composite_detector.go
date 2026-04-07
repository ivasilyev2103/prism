package privacy

import (
	"context"
	"fmt"
	"sort"
)

type compositeDetector struct {
	detectors []Detector
}

// NewCompositeDetector creates a detector that chains multiple detectors
// and merges their results (union of entities, max score per overlapping span).
func NewCompositeDetector(detectors ...Detector) Detector {
	return &compositeDetector{detectors: detectors}
}

func (cd *compositeDetector) Detect(ctx context.Context, text string, profile Profile) ([]Entity, error) {
	if profile == ProfileOff {
		return nil, nil
	}

	var allEntities []Entity
	for _, d := range cd.detectors {
		entities, err := d.Detect(ctx, text, profile)
		if err != nil {
			return nil, fmt.Errorf("composite detector: %w", err)
		}
		allEntities = append(allEntities, entities...)
	}
	return mergeEntities(allEntities), nil
}

func (cd *compositeDetector) HealthCheck(ctx context.Context) error {
	for _, d := range cd.detectors {
		if err := d.HealthCheck(ctx); err != nil {
			return err
		}
	}
	return nil
}

// mergeEntities performs union of entities with max-score-per-overlap deduplication.
// When two entities overlap in position, the one with the higher score is kept.
func mergeEntities(entities []Entity) []Entity {
	if len(entities) == 0 {
		return nil
	}

	// Sort by start position ascending, then by score descending.
	sort.Slice(entities, func(i, j int) bool {
		if entities[i].Start != entities[j].Start {
			return entities[i].Start < entities[j].Start
		}
		return entities[i].Score > entities[j].Score
	})

	merged := make([]Entity, 0, len(entities))
	merged = append(merged, entities[0])

	for i := 1; i < len(entities); i++ {
		last := &merged[len(merged)-1]
		curr := entities[i]

		if curr.Start < last.End {
			// Overlapping spans — keep the higher-scored entity.
			if curr.Score > last.Score {
				*last = curr
			}
			// Extend the span if the current entity reaches further.
			if curr.End > last.End {
				last.End = curr.End
			}
			continue
		}
		merged = append(merged, curr)
	}

	return merged
}
