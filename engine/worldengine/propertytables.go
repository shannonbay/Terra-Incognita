package worldengine

// PropertyTables is the ECS-style columnar storage layer for entity resources.
//
// Conceptually, each (typeName, fieldName) pair maps to a contiguous slice
// indexed by an integer "slot" assigned to each entity at spawn time. This
// enables cache-friendly iteration when the tick scheduler processes all entities
// of a given type in sequence.
//
// The ResourceTable (resources.go) is the canonical runtime store. PropertyTables
// is an additional index that gives the tick scheduler and action dispatcher fast
// access to type-homogeneous subsets of entity state. It is populated from the
// ResourceTable at startup and kept in sync during simulation.
//
// Phase 4 (tick engine) uses PropertyTables for the execution hot path.
// Phase 2 only needs to define the structure; full population is done in Phase 4.

// column holds a contiguous slice of values for one (type, field) pair.
type column struct {
	typeName string
	field    string
	values   []any     // indexed by slot
	entityIDs []string // slot → entityID (parallel to values)
}

// PropertyTables is the columnar ECS store.
type PropertyTables struct {
	// columns: key is "typeName\x00fieldName"
	columns map[string]*column

	// slots: entityID → slot index within its type column
	slots map[string]int

	// typeSlots: typeName → ordered list of entityIDs (defines slot order)
	typeSlots map[string][]string
}

// NewPropertyTables returns an empty PropertyTables.
func NewPropertyTables() *PropertyTables {
	return &PropertyTables{
		columns:   make(map[string]*column),
		slots:     make(map[string]int),
		typeSlots: make(map[string][]string),
	}
}

func columnKey(typeName, field string) string {
	return typeName + "\x00" + field
}

// RegisterType ensures columns exist for all fields of the given type.
// Called when a type is first spawned.
func (pt *PropertyTables) RegisterType(typeName string, fields []string) {
	for _, field := range fields {
		key := columnKey(typeName, field)
		if _, exists := pt.columns[key]; !exists {
			pt.columns[key] = &column{typeName: typeName, field: field}
		}
	}
}

// AddEntity assigns a slot to entityID within typeName and initialises all
// column entries to their zero values.
func (pt *PropertyTables) AddEntity(entityID, typeName string, resources map[string]any) {
	slot := len(pt.typeSlots[typeName])
	pt.typeSlots[typeName] = append(pt.typeSlots[typeName], entityID)
	pt.slots[entityID] = slot

	for _, col := range pt.columns {
		if col.typeName != typeName {
			continue
		}
		// Extend slice to cover the new slot.
		for len(col.values) <= slot {
			col.values = append(col.values, nil)
			col.entityIDs = append(col.entityIDs, "")
		}
		col.entityIDs[slot] = entityID
		if v, ok := resources[col.field]; ok {
			col.values[slot] = v
		}
	}
}

// SetValue updates the value for (entityID, field) in the column, if it exists.
// Called by the ResourceTable interceptor to keep the columnar view in sync.
func (pt *PropertyTables) SetValue(entityID, typeName, field string, value any) {
	key := columnKey(typeName, field)
	col, ok := pt.columns[key]
	if !ok {
		return
	}
	slot, ok := pt.slots[entityID]
	if !ok || slot >= len(col.values) {
		return
	}
	col.values[slot] = value
}

// GetValue returns the columnar value for (entityID, typeName, field).
// Falls back to nil if not found.
func (pt *PropertyTables) GetValue(entityID, typeName, field string) (any, bool) {
	key := columnKey(typeName, field)
	col, ok := pt.columns[key]
	if !ok {
		return nil, false
	}
	slot, ok := pt.slots[entityID]
	if !ok || slot >= len(col.values) {
		return nil, false
	}
	return col.values[slot], true
}

// Column returns the column for (typeName, field), giving the tick scheduler
// direct access to the full type-homogeneous slice.
func (pt *PropertyTables) Column(typeName, field string) *column {
	return pt.columns[columnKey(typeName, field)]
}

// EntitiesOfType returns the ordered slice of entity IDs for typeName.
func (pt *PropertyTables) EntitiesOfType(typeName string) []string {
	return pt.typeSlots[typeName]
}

// RemoveEntity reclaims the entity's slot. Slots are compacted lazily —
// removed entity slots are tombstoned (entityID set to "") and excluded
// from iteration by the tick scheduler.
func (pt *PropertyTables) RemoveEntity(entityID, typeName string) {
	slot, ok := pt.slots[entityID]
	if !ok {
		return
	}
	// Tombstone in typeSlots
	if slots := pt.typeSlots[typeName]; slot < len(slots) {
		pt.typeSlots[typeName][slot] = ""
	}
	// Tombstone in columns
	for _, col := range pt.columns {
		if col.typeName != typeName {
			continue
		}
		if slot < len(col.entityIDs) {
			col.entityIDs[slot] = ""
			col.values[slot] = nil
		}
	}
	delete(pt.slots, entityID)
}

// Snapshot returns a deep copy of all column values.
func (pt *PropertyTables) Snapshot() map[string][]any {
	snap := make(map[string][]any, len(pt.columns))
	for key, col := range pt.columns {
		c := make([]any, len(col.values))
		for i, v := range col.values {
			c[i] = deepCopyValue(v)
		}
		snap[key] = c
	}
	return snap
}

// Restore replaces column values from a snapshot.
func (pt *PropertyTables) Restore(snap map[string][]any) {
	for key, values := range snap {
		col, ok := pt.columns[key]
		if !ok {
			continue
		}
		c := make([]any, len(values))
		for i, v := range values {
			c[i] = deepCopyValue(v)
		}
		col.values = c
	}
}
