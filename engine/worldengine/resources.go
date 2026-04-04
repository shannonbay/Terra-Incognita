package worldengine

import "fmt"

// -----------------------------------------------------------------------------
// Visibility
// -----------------------------------------------------------------------------

// VisibilityLevel controls what external agents can see for a resource.
type VisibilityLevel int

const (
	// VisibilityPublic resources are visible to any entity or agent. Default.
	VisibilityPublic VisibilityLevel = iota

	// VisibilityHidden resources are visible to tick functions but not to
	// external agent perception. Used to create information asymmetry.
	VisibilityHidden

	// VisibilityPrivate resources are visible only to the owning entity's own
	// tick function — not to other entities' tick functions or agents.
	VisibilityPrivate
)

// -----------------------------------------------------------------------------
// Complex resource value types
// -----------------------------------------------------------------------------

// Set is an unordered collection of unique strings.
type Set struct {
	items map[string]struct{}
}

// NewSet returns an empty Set.
func NewSet() Set {
	return Set{items: make(map[string]struct{})}
}

// Add inserts item into the set.
func (s *Set) Add(item string) {
	if s.items == nil {
		s.items = make(map[string]struct{})
	}
	s.items[item] = struct{}{}
}

// Has reports whether item is in the set.
func (s Set) Has(item string) bool {
	_, ok := s.items[item]
	return ok
}

// Remove deletes item from the set (no-op if absent).
func (s *Set) Remove(item string) {
	delete(s.items, item)
}

// Len returns the number of elements.
func (s Set) Len() int { return len(s.items) }

// Slice returns all items as a sorted slice (deterministic ordering).
func (s Set) Slice() []string {
	out := make([]string, 0, len(s.items))
	for k := range s.items {
		out = append(out, k)
	}
	// sort for determinism
	sortStrings(out)
	return out
}

// Clone returns a deep copy.
func (s Set) Clone() Set {
	c := NewSet()
	for k := range s.items {
		c.items[k] = struct{}{}
	}
	return c
}

// Queue is a FIFO collection of arbitrary values.
type Queue struct {
	items []any
}

// Push appends item to the back of the queue.
func (q *Queue) Push(item any) {
	q.items = append(q.items, item)
}

// Pop removes and returns the front item. Panics if empty.
func (q *Queue) Pop() any {
	if len(q.items) == 0 {
		panic("worldengine: Queue.Pop on empty queue")
	}
	v := q.items[0]
	q.items = q.items[1:]
	return v
}

// Len returns the number of items.
func (q Queue) Len() int { return len(q.items) }

// Slice returns a copy of the queue contents as a slice.
func (q Queue) Slice() []any {
	out := make([]any, len(q.items))
	copy(out, q.items)
	return out
}

// Clone returns a deep copy.
func (q Queue) Clone() Queue {
	c := Queue{items: make([]any, len(q.items))}
	copy(c.items, q.items)
	return c
}

// -----------------------------------------------------------------------------
// Resource key
// -----------------------------------------------------------------------------

type resourceKey struct {
	entityID string
	field    string
}

// -----------------------------------------------------------------------------
// ResourceTable
// -----------------------------------------------------------------------------

// ResourceTable is the runtime store for all entity resource and parameter values.
// It is backed by a flat map for correctness; Phase 4 (PropertyTables) provides
// a columnar layout for hot-path performance.
//
// The ResourceTable also buffers delta records for every mutation so that the
// event log can capture before/after values without the world author doing anything.
type ResourceTable struct {
	values map[resourceKey]any // current values
	params map[resourceKey]any // immutable parameters
	deltas []Delta             // buffered mutations for this tick
}

func newResourceTable() *ResourceTable {
	return &ResourceTable{
		values: make(map[resourceKey]any),
		params: make(map[resourceKey]any),
	}
}

// SetResource sets a resource value, buffering a delta.
// oldValue is the value before the mutation (caller must supply it for the delta).
func (rt *ResourceTable) SetResource(entityID, field string, value any) {
	k := resourceKey{entityID, field}
	old := rt.values[k]
	rt.values[k] = value
	rt.deltas = append(rt.deltas, Delta{
		EntityID: entityID,
		Field:    field,
		OldValue: old,
		NewValue: value,
	})
}

// GetResource returns the current value for (entityID, field).
// ok is false if the field does not exist.
func (rt *ResourceTable) GetResource(entityID, field string) (any, bool) {
	v, ok := rt.values[resourceKey{entityID, field}]
	return v, ok
}

// HasResource reports whether (entityID, field) is present.
func (rt *ResourceTable) HasResource(entityID, field string) bool {
	_, ok := rt.values[resourceKey{entityID, field}]
	return ok
}

// GetFloat returns a float64 resource. Panics if absent or wrong type.
func (rt *ResourceTable) GetFloat(entityID, field string) float64 {
	v, ok := rt.GetResource(entityID, field)
	if !ok {
		panic(fmt.Sprintf("worldengine: entity %q has no resource %q", entityID, field))
	}
	f, ok := v.(float64)
	if !ok {
		panic(fmt.Sprintf("worldengine: resource %q on %q is not float64", field, entityID))
	}
	return f
}

// GetFloatOr returns a float64 resource, or def if absent.
func (rt *ResourceTable) GetFloatOr(entityID, field string, def float64) float64 {
	v, ok := rt.GetResource(entityID, field)
	if !ok {
		return def
	}
	f, ok := v.(float64)
	if !ok {
		return def
	}
	return f
}

// SetParam stores an immutable parameter value (no delta recorded).
func (rt *ResourceTable) SetParam(entityID, field string, value any) {
	rt.params[resourceKey{entityID, field}] = value
}

// GetParam returns an immutable parameter. Panics if absent.
func (rt *ResourceTable) GetParam(entityID, field string) any {
	v, ok := rt.params[resourceKey{entityID, field}]
	if !ok {
		panic(fmt.Sprintf("worldengine: entity %q has no param %q", entityID, field))
	}
	return v
}

// GetParamFloat returns a float64 parameter. Panics if absent or wrong type.
func (rt *ResourceTable) GetParamFloat(entityID, field string) float64 {
	v := rt.GetParam(entityID, field)
	f, ok := v.(float64)
	if !ok {
		panic(fmt.Sprintf("worldengine: param %q on %q is not float64", field, entityID))
	}
	return f
}

// FlushDeltas returns all buffered deltas and resets the buffer.
func (rt *ResourceTable) FlushDeltas() []Delta {
	out := rt.deltas
	rt.deltas = nil
	return out
}

// Snapshot returns a deep copy of all current resource values.
func (rt *ResourceTable) Snapshot() map[resourceKey]any {
	snap := make(map[resourceKey]any, len(rt.values))
	for k, v := range rt.values {
		snap[k] = deepCopyValue(v)
	}
	return snap
}

// Restore replaces current values with a snapshot.
func (rt *ResourceTable) Restore(snap map[resourceKey]any) {
	rt.values = make(map[resourceKey]any, len(snap))
	for k, v := range snap {
		rt.values[k] = deepCopyValue(v)
	}
}

// InitEntity seeds a new entity's resources and params from type defaults and init overrides.
// Resources from init override type defaults; params from init override type defaults.
func (rt *ResourceTable) InitEntity(entityID string, defaultResources, initResources, defaultParams, initParams P) {
	merged := func(defaults, overrides P) P {
		out := make(P, len(defaults))
		for k, v := range defaults {
			out[k] = v
		}
		for k, v := range overrides {
			out[k] = v
		}
		return out
	}

	resources := merged(defaultResources, initResources)
	params := merged(defaultParams, initParams)

	for field, value := range resources {
		rt.values[resourceKey{entityID, field}] = value
	}
	for field, value := range params {
		rt.params[resourceKey{entityID, field}] = value
	}
}

// RemoveEntity deletes all resource and param entries for entityID.
func (rt *ResourceTable) RemoveEntity(entityID string) {
	for k := range rt.values {
		if k.entityID == entityID {
			delete(rt.values, k)
		}
	}
	for k := range rt.params {
		if k.entityID == entityID {
			delete(rt.params, k)
		}
	}
}

// AllResources returns all resource field names and their current values for entityID.
func (rt *ResourceTable) AllResources(entityID string) map[string]any {
	out := make(map[string]any)
	for k, v := range rt.values {
		if k.entityID == entityID {
			out[k.field] = v
		}
	}
	return out
}

// AllParams returns all param field names and their values for entityID.
func (rt *ResourceTable) AllParams(entityID string) map[string]any {
	out := make(map[string]any)
	for k, v := range rt.params {
		if k.entityID == entityID {
			out[k.field] = v
		}
	}
	return out
}

// -----------------------------------------------------------------------------
// Delta record
// -----------------------------------------------------------------------------

// Delta records a single resource mutation for the event log.
type Delta struct {
	EntityID string
	Field    string
	OldValue any
	NewValue any
}

// -----------------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------------

func deepCopyValue(v any) any {
	switch val := v.(type) {
	case []any:
		c := make([]any, len(val))
		copy(c, val)
		return c
	case map[string]any:
		c := make(map[string]any, len(val))
		for k, v2 := range val {
			c[k] = v2
		}
		return c
	case Set:
		return val.Clone()
	case Queue:
		return val.Clone()
	default:
		return v
	}
}

// sortStrings is a simple insertion sort to avoid importing sort in hot paths.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		key := s[i]
		j := i - 1
		for j >= 0 && s[j] > key {
			s[j+1] = s[j]
			j--
		}
		s[j+1] = key
	}
}
