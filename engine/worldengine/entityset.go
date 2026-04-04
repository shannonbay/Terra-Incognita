package worldengine

import "math"

// EntitySet is an ordered collection of entities returned by traversal and
// filter operations. It is the return type of Neighbors(), Contains(), and
// query results.
type EntitySet struct {
	entities    []*Entity
	connWeights map[string]float64 // entityID → min connection weight (for Nearest)
}

// Filter returns a new EntitySet containing only entities matching f.
func (s EntitySet) Filter(f Filter) EntitySet {
	var out EntitySet
	for _, e := range s.entities {
		if f.Type != "" && e.typeName != f.Type {
			continue
		}
		if f.Predicate != nil && !f.Predicate(e) {
			continue
		}
		out.entities = append(out.entities, e)
		if s.connWeights != nil {
			if out.connWeights == nil {
				out.connWeights = make(map[string]float64)
			}
			if w, ok := s.connWeights[e.id]; ok {
				out.connWeights[e.id] = w
			}
		}
	}
	return out
}

// MaxBy returns the entity with the highest value of the named resource, or nil.
func (s EntitySet) MaxBy(resource string) *Entity {
	var best *Entity
	bestVal := math.Inf(-1)
	for _, e := range s.entities {
		v := e.GetOr(resource, 0)
		if v > bestVal {
			best = e
			bestVal = v
		}
	}
	return best
}

// MinBy returns the entity with the lowest value of the named resource, or nil.
func (s EntitySet) MinBy(resource string) *Entity {
	var best *Entity
	bestVal := math.Inf(1)
	for _, e := range s.entities {
		v := e.GetOr(resource, 0)
		if v < bestVal {
			best = e
			bestVal = v
		}
	}
	return best
}

// Nearest returns the entity with the smallest connection weight, or nil.
// Falls back to first entity if no weight data is available.
func (s EntitySet) Nearest() *Entity {
	if len(s.entities) == 0 {
		return nil
	}
	if s.connWeights == nil {
		return s.entities[0]
	}
	var best *Entity
	bestWeight := math.Inf(1)
	for _, e := range s.entities {
		w, ok := s.connWeights[e.id]
		if !ok {
			w = math.Inf(1)
		}
		if w < bestWeight {
			best = e
			bestWeight = w
		}
	}
	if best == nil {
		return s.entities[0]
	}
	return best
}

// Count returns the number of entities in the set.
func (s EntitySet) Count() int { return len(s.entities) }

// Each calls fn for every entity in the set.
func (s EntitySet) Each(fn func(e *Entity)) {
	for _, e := range s.entities {
		fn(e)
	}
}

// Sum returns the total of the named float64 resource across all entities.
func (s EntitySet) Sum(resource string) float64 {
	var total float64
	for _, e := range s.entities {
		total += e.GetOr(resource, 0)
	}
	return total
}

// Avg returns the average of the named float64 resource, or 0 if empty.
func (s EntitySet) Avg(resource string) float64 {
	if len(s.entities) == 0 {
		return 0
	}
	return s.Sum(resource) / float64(len(s.entities))
}

// Entities returns the raw entity slice (for engine-internal use).
func (s EntitySet) Entities() []*Entity {
	return s.entities
}

// NewEntitySet constructs an EntitySet from a slice of entities.
// Used in tests and engine internals.
func NewEntitySet(entities []*Entity) EntitySet {
	return EntitySet{entities: entities}
}
