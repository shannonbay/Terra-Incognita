package worldengine

import "fmt"

// TypeRegistry maps type names to their TypeDef. It is the engine-internal
// lookup table used by the tick scheduler, action dispatcher, and agent dispatcher.
type TypeRegistry struct {
	types map[string]*TypeDef
}

func newTypeRegistry() *TypeRegistry {
	return &TypeRegistry{types: make(map[string]*TypeDef)}
}

// Register adds a TypeDef. Panics if the name is already registered.
func (r *TypeRegistry) Register(t *TypeDef) {
	if _, exists := r.types[t.name]; exists {
		panic("worldengine: type already registered: " + t.name)
	}
	r.types[t.name] = t
}

// Get returns the TypeDef for name, or an error if not found.
func (r *TypeRegistry) Get(name string) (*TypeDef, error) {
	t, ok := r.types[name]
	if !ok {
		return nil, fmt.Errorf("worldengine: unknown type %q", name)
	}
	return t, nil
}

// MustGet returns the TypeDef for name, panicking if not found.
func (r *TypeRegistry) MustGet(name string) *TypeDef {
	t, err := r.Get(name)
	if err != nil {
		panic(err.Error())
	}
	return t
}

// TickFn returns the tick function for typeName, or nil if the type has none.
func (r *TypeRegistry) TickFn(typeName string) func(e *Entity, dt float64) {
	t, ok := r.types[typeName]
	if !ok {
		return nil
	}
	return t.tickFn
}

// ActionHandler returns the action function for (typeName, actionName).
// Returns nil, false if not found.
func (r *TypeRegistry) ActionHandler(typeName, actionName string) (func(target *Entity, invoker *Entity, p P) ActionResult, bool) {
	t, ok := r.types[typeName]
	if !ok {
		return nil, false
	}
	fn, ok := t.actions[actionName]
	return fn, ok
}

// IsRemoteAction reports whether actionName on typeName is declared remote.
func (r *TypeRegistry) IsRemoteAction(typeName, actionName string) bool {
	t, ok := r.types[typeName]
	if !ok {
		return false
	}
	return t.remoteActions[actionName]
}

// DefaultParams returns a copy of the type's default param map.
func (r *TypeRegistry) DefaultParams(typeName string) P {
	t, ok := r.types[typeName]
	if !ok {
		return P{}
	}
	out := make(P, len(t.params))
	for k, v := range t.params {
		out[k] = v
	}
	return out
}

// DefaultResources returns a copy of the type's default resource map.
func (r *TypeRegistry) DefaultResources(typeName string) P {
	t, ok := r.types[typeName]
	if !ok {
		return P{}
	}
	out := make(P, len(t.resources))
	for k, v := range t.resources {
		out[k] = v
	}
	return out
}

// Visibility returns the visibility level for a resource on a type.
func (r *TypeRegistry) Visibility(typeName, resource string) VisibilityLevel {
	t, ok := r.types[typeName]
	if !ok {
		return VisibilityPublic
	}
	if t.private[resource] {
		return VisibilityPrivate
	}
	if t.hidden[resource] {
		return VisibilityHidden
	}
	return VisibilityPublic
}

// AgentConfig returns the AgentConfig for a type, or nil if it has none.
func (r *TypeRegistry) AgentConfig(typeName string) *AgentConfig {
	t, ok := r.types[typeName]
	if !ok {
		return nil
	}
	return t.agentCfg
}

// Names returns all registered type names in no particular order.
func (r *TypeRegistry) Names() []string {
	names := make([]string, 0, len(r.types))
	for k := range r.types {
		names = append(names, k)
	}
	return names
}
