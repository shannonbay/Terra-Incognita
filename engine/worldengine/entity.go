package worldengine

import "fmt"

// Entity is the core simulation object. Every thing in the world — a fishing
// ground, a boat, a port — is an Entity.
//
// The Entity is a thin facade over the World's ResourceTable and connection graph.
// All state mutations go through this facade so that deltas are automatically
// captured for the event log.
//
// During a tick, entities queue actions and lifecycle events rather than executing
// them immediately. The engine processes these queues after all tick functions run.
type Entity struct {
	id       string
	typeName string
	world    *World
}

// ID returns the entity's unique identifier.
func (e *Entity) ID() string { return e.id }

// Type returns the entity's type name.
func (e *Entity) Type() string { return e.typeName }

// -----------------------------------------------------------------------------
// Scalar resource access
// -----------------------------------------------------------------------------

// Get returns a resource value as float64. Panics if the resource does not exist
// or is not a float64.
func (e *Entity) Get(resource string) float64 {
	return e.world.resources.GetFloat(e.id, resource)
}

// GetOr returns a resource value as float64, or def if the resource does not exist
// or is not a float64. This is the safe accessor used in action guards.
func (e *Entity) GetOr(resource string, def float64) float64 {
	return e.world.resources.GetFloatOr(e.id, resource, def)
}

// Set sets a resource to a float64 value, buffering a delta.
func (e *Entity) Set(resource string, value float64) {
	e.world.resources.SetResource(e.id, resource, value)
}

// Has reports whether the entity has the named resource.
func (e *Entity) Has(resource string) bool {
	return e.world.resources.HasResource(e.id, resource)
}

// Param returns an immutable parameter value as float64.
// Panics if absent or not float64.
func (e *Entity) Param(name string) float64 {
	return e.world.resources.GetParamFloat(e.id, name)
}

// -----------------------------------------------------------------------------
// Complex resource accessors
// -----------------------------------------------------------------------------

// ListPush appends item to the list resource. Panics if resource is not a list.
func (e *Entity) ListPush(resource string, item any) {
	v, ok := e.world.resources.GetResource(e.id, resource)
	if !ok {
		v = []any{}
	}
	list, ok := v.([]any)
	if !ok {
		panic(fmt.Sprintf("worldengine: resource %q on %q is not a list", resource, e.id))
	}
	e.world.resources.SetResource(e.id, resource, append(list, item))
}

// ListPop removes and returns the last item from the list resource.
// Panics if resource is not a list or is empty.
func (e *Entity) ListPop(resource string) any {
	v, ok := e.world.resources.GetResource(e.id, resource)
	if !ok {
		panic(fmt.Sprintf("worldengine: resource %q on %q does not exist", resource, e.id))
	}
	list, ok := v.([]any)
	if !ok {
		panic(fmt.Sprintf("worldengine: resource %q on %q is not a list", resource, e.id))
	}
	if len(list) == 0 {
		panic(fmt.Sprintf("worldengine: ListPop on empty list %q on %q", resource, e.id))
	}
	item := list[len(list)-1]
	e.world.resources.SetResource(e.id, resource, list[:len(list)-1])
	return item
}

// QueuePush appends item to the back of the queue resource.
func (e *Entity) QueuePush(resource string, item any) {
	v, ok := e.world.resources.GetResource(e.id, resource)
	var q Queue
	if ok {
		q, ok = v.(Queue)
		if !ok {
			panic(fmt.Sprintf("worldengine: resource %q on %q is not a Queue", resource, e.id))
		}
	}
	q.Push(item)
	e.world.resources.SetResource(e.id, resource, q)
}

// QueuePop removes and returns the front item of the queue resource.
// Panics if empty.
func (e *Entity) QueuePop(resource string) any {
	v, ok := e.world.resources.GetResource(e.id, resource)
	if !ok {
		panic(fmt.Sprintf("worldengine: resource %q on %q does not exist", resource, e.id))
	}
	q, ok := v.(Queue)
	if !ok {
		panic(fmt.Sprintf("worldengine: resource %q on %q is not a Queue", resource, e.id))
	}
	item := q.Pop()
	e.world.resources.SetResource(e.id, resource, q)
	return item
}

// SetAdd adds item to the set resource.
func (e *Entity) SetAdd(resource string, item string) {
	v, ok := e.world.resources.GetResource(e.id, resource)
	var s Set
	if ok {
		s, ok = v.(Set)
		if !ok {
			panic(fmt.Sprintf("worldengine: resource %q on %q is not a Set", resource, e.id))
		}
	} else {
		s = NewSet()
	}
	s.Add(item)
	e.world.resources.SetResource(e.id, resource, s)
}

// SetHas reports whether item is in the set resource.
func (e *Entity) SetHas(resource string, item string) bool {
	v, ok := e.world.resources.GetResource(e.id, resource)
	if !ok {
		return false
	}
	s, ok := v.(Set)
	if !ok {
		return false
	}
	return s.Has(item)
}

// MapSet sets key→value in the map resource.
func (e *Entity) MapSet(resource string, key string, value any) {
	v, ok := e.world.resources.GetResource(e.id, resource)
	var m map[string]any
	if ok {
		m, ok = v.(map[string]any)
		if !ok {
			panic(fmt.Sprintf("worldengine: resource %q on %q is not a map", resource, e.id))
		}
		// copy before mutating to preserve delta old value
		nm := make(map[string]any, len(m)+1)
		for k, v2 := range m {
			nm[k] = v2
		}
		m = nm
	} else {
		m = make(map[string]any)
	}
	m[key] = value
	e.world.resources.SetResource(e.id, resource, m)
}

// MapGet returns the value for key in the map resource, or nil if absent.
func (e *Entity) MapGet(resource string, key string) any {
	v, ok := e.world.resources.GetResource(e.id, resource)
	if !ok {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return m[key]
}

// -----------------------------------------------------------------------------
// Spatial awareness
// -----------------------------------------------------------------------------

// Location returns the entity this one is contained inside, or nil.
func (e *Entity) Location() *Entity {
	loc := e.world.graph.LocationOf(e.id)
	if loc == "" {
		return nil
	}
	return e.world.entities[loc]
}

// Contains returns the set of entities directly inside this one.
func (e *Entity) Contains() EntitySet {
	ids := e.world.graph.ContainedBy(e.id)
	entities := make([]*Entity, 0, len(ids))
	for _, id := range ids {
		if ent, ok := e.world.entities[id]; ok {
			entities = append(entities, ent)
		}
	}
	return EntitySet{entities: entities}
}

// Neighbors returns entities connected to this one, optionally filtered by Filter.
// If multiple filters are provided they are ANDed together.
func (e *Entity) Neighbors(filters ...Filter) EntitySet {
	conns := e.world.graph.ConnectionsFrom(e.id)
	seen := make(map[string]bool)
	var entities []*Entity

	for _, conn := range conns {
		if seen[conn.To] {
			continue
		}
		ent, ok := e.world.entities[conn.To]
		if !ok {
			continue
		}
		match := true
		for _, f := range filters {
			if f.Type != "" && ent.typeName != f.Type {
				match = false
				break
			}
			if f.ConnType != "" && conn.Type != f.ConnType {
				match = false
				break
			}
			if f.Predicate != nil && !f.Predicate(ent) {
				match = false
				break
			}
		}
		if match {
			seen[conn.To] = true
			entities = append(entities, ent)
		}
	}
	return EntitySet{entities: entities, connWeights: connWeightsFor(e.id, conns, e.world.entities)}
}

func connWeightsFor(fromID string, conns []Connection, entities map[string]*Entity) map[string]float64 {
	m := make(map[string]float64, len(conns))
	for _, c := range conns {
		if _, ok := entities[c.To]; ok {
			if existing, has := m[c.To]; !has || c.Weight < existing {
				m[c.To] = c.Weight
			}
		}
	}
	return m
}

// -----------------------------------------------------------------------------
// Actions
// -----------------------------------------------------------------------------

// Act queues an action invocation. Actions are collected and dispatched by the
// engine after all tick functions complete.
//
// When params contains "target" (a string entity ID), the action targets that entity.
// Otherwise the action targets the invoker's current location or the invoker itself.
func (e *Entity) Act(name string, params P) {
	e.world.queueAction(pendingAction{
		invokerID: e.id,
		name:      name,
		params:    params,
	})
}

// -----------------------------------------------------------------------------
// Entity lifecycle (queued — processed at end of tick)
// -----------------------------------------------------------------------------

// Spawn queues creation of a new entity from an existing type.
// The new entity is available at the start of the next tick.
func (e *Entity) Spawn(id string, typeName string, init Init) {
	e.world.queueSpawn(pendingSpawn{
		id:        id,
		typeName:  typeName,
		init:      init,
		spawnedBy: e.id,
	})
}

// Destroy queues removal of another entity at end of this tick.
func (e *Entity) Destroy(id string) {
	e.world.queueDestroy(pendingDestroy{
		id:          id,
		destroyedBy: e.id,
	})
}

// DestroySelf queues removal of this entity at end of this tick.
func (e *Entity) DestroySelf() {
	e.Destroy(e.id)
}

// -----------------------------------------------------------------------------
// Mutable connections (queued — processed at end of tick)
// -----------------------------------------------------------------------------

// ConnectTo queues addition of a bidirectional connection from this entity to toID.
func (e *Entity) ConnectTo(toID string, connType string, weight float64) {
	e.world.queueConnectTo(pendingConnect{
		fromID:   e.id,
		toID:     toID,
		connType: connType,
		weight:   weight,
	})
}

// Disconnect queues removal of a connection from this entity to toID.
func (e *Entity) Disconnect(toID string, connType string) {
	e.world.queueDisconnect(pendingDisconnect{
		fromID:   e.id,
		toID:     toID,
		connType: connType,
	})
}

// -----------------------------------------------------------------------------
// Query
// -----------------------------------------------------------------------------

// Query executes a query-language expression scoped to this entity's visibility.
func (e *Entity) Query(expr string) any {
	return e.world.entityQuery(e.id, expr)
}
