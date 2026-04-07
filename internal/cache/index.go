package cache

import "sync"

// vectorIndex is a VP-tree (Vantage Point tree) providing O(log n) nearest
// neighbour search by cosine distance.
type vectorIndex struct {
	mu      sync.RWMutex
	entries []indexEntry
	root    *vpNode
	dirty   bool
}

type indexEntry struct {
	id        string
	embedding []float32
}

type vpNode struct {
	idx    int     // index into entries
	radius float32 // median distance to children
	left   *vpNode // closer than radius
	right  *vpNode // farther or equal
}

func newVectorIndex() *vectorIndex {
	return &vectorIndex{}
}

// add appends an entry and marks the tree for rebuild.
func (vi *vectorIndex) add(id string, emb []float32) {
	vi.mu.Lock()
	defer vi.mu.Unlock()
	vi.entries = append(vi.entries, indexEntry{id: id, embedding: emb})
	vi.dirty = true
}

// search finds all entries within maxDistance of the query vector.
// Rebuilds the VP-tree if it has been modified since the last build.
func (vi *vectorIndex) search(query []float32, maxDist float32) []indexEntry {
	vi.mu.Lock()
	if vi.dirty {
		vi.rebuild()
		vi.dirty = false
	}
	vi.mu.Unlock()

	vi.mu.RLock()
	defer vi.mu.RUnlock()

	if vi.root == nil {
		return nil
	}
	var results []indexEntry
	vi.searchNode(vi.root, query, maxDist, &results)
	return results
}

func (vi *vectorIndex) searchNode(n *vpNode, query []float32, maxDist float32, results *[]indexEntry) {
	if n == nil {
		return
	}
	e := vi.entries[n.idx]
	dist := cosineDistance(query, e.embedding)

	if dist <= maxDist {
		*results = append(*results, e)
	}

	// Prune branches that cannot contain matches.
	if n.left != nil && dist-maxDist < n.radius {
		vi.searchNode(n.left, query, maxDist, results)
	}
	if n.right != nil && dist+maxDist >= n.radius {
		vi.searchNode(n.right, query, maxDist, results)
	}
}

// rebuild constructs the VP-tree from the current entries.
func (vi *vectorIndex) rebuild() {
	if len(vi.entries) == 0 {
		vi.root = nil
		return
	}
	indices := make([]int, len(vi.entries))
	for i := range indices {
		indices[i] = i
	}
	vi.root = vi.buildNode(indices)
}

func (vi *vectorIndex) buildNode(indices []int) *vpNode {
	if len(indices) == 0 {
		return nil
	}
	if len(indices) == 1 {
		return &vpNode{idx: indices[0]}
	}

	// Pick first element as vantage point.
	vpIdx := indices[0]
	rest := indices[1:]

	// Compute distances from vantage point to all others.
	dists := make([]float32, len(rest))
	for i, idx := range rest {
		dists[i] = cosineDistance(vi.entries[vpIdx].embedding, vi.entries[idx].embedding)
	}

	// Find median distance.
	median := selectMedian(dists)

	// Partition into left (< median) and right (>= median).
	var leftIdx, rightIdx []int
	for i, d := range dists {
		if d < median {
			leftIdx = append(leftIdx, rest[i])
		} else {
			rightIdx = append(rightIdx, rest[i])
		}
	}

	return &vpNode{
		idx:    vpIdx,
		radius: median,
		left:   vi.buildNode(leftIdx),
		right:  vi.buildNode(rightIdx),
	}
}

// remove marks an entry for removal. Entries are filtered on rebuild.
func (vi *vectorIndex) removeByProject(projectID string, lookup func(id string) string) {
	vi.mu.Lock()
	defer vi.mu.Unlock()

	filtered := vi.entries[:0]
	for _, e := range vi.entries {
		if lookup(e.id) != projectID {
			filtered = append(filtered, e)
		}
	}
	vi.entries = filtered
	vi.dirty = true
}

// size returns the number of indexed entries.
func (vi *vectorIndex) size() int {
	vi.mu.RLock()
	defer vi.mu.RUnlock()
	return len(vi.entries)
}

// selectMedian returns the approximate median of a float32 slice.
// Uses a simple O(n) approach by averaging the two middle values.
func selectMedian(vals []float32) float32 {
	if len(vals) == 0 {
		return 0
	}
	// Copy to avoid modifying the original.
	tmp := make([]float32, len(vals))
	copy(tmp, vals)
	// Simple insertion sort (entries are typically small per node).
	for i := 1; i < len(tmp); i++ {
		key := tmp[i]
		j := i - 1
		for j >= 0 && tmp[j] > key {
			tmp[j+1] = tmp[j]
			j--
		}
		tmp[j+1] = key
	}
	return tmp[len(tmp)/2]
}
