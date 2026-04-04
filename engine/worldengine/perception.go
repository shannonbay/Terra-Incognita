package worldengine

// BuildPerception evaluates an agent's perception query list and assembles the
// perception map sent in the POST /decide request body. Visibility enforcement
// (hidden/private resource stripping) is delegated to QueryAgentPerception.
//
// The result is keyed by the query path. Each value is a JSON-serialisable
// representation of the query result: entity lists become []map, single entities
// become map, scalars stay as-is.
func BuildPerception(w *World, agentID string, cfg *AgentConfig) map[string]any {
	perception := make(map[string]any)

	// Always include /self as own resources (no visibility filter for self).
	if e, ok := w.entities[agentID]; ok {
		perception["/self"] = w.resources.AllResources(e.id)
	}

	// Always include /self/available_actions.
	if result, err := w.QueryAgentPerception("/self/available_actions", agentID); err == nil && result != nil {
		perception["/self/available_actions"] = result
	}

	// Always include /world/config.
	if result, err := w.QueryAgentPerception("/config", agentID); err == nil && result != nil {
		perception["/world/config"] = result
	}

	// Score visibility entries.
	for k, v := range w.scorePerceptionEntries(agentID) {
		perception[k] = v
	}

	// Evaluate configured perception queries.
	for _, query := range cfg.Perception {
		result, err := w.QueryAgentPerception(query, agentID)
		if err != nil || result == nil {
			continue
		}
		perception[query] = serializeEntities(result, w, agentID)
	}

	return perception
}

// serializeEntities converts a query result to a JSON-serialisable form for the
// perception map. Entity objects are projected to maps with id, type, and
// (agent-visible) resources.
func serializeEntities(result any, w *World, agentID string) any {
	switch v := result.(type) {
	case []*Entity:
		if len(v) == 0 {
			return nil
		}
		out := make([]map[string]any, 0, len(v))
		for _, e := range v {
			out = append(out, entityPerceptionMap(e, w, agentID))
		}
		if len(out) == 1 {
			return out[0]
		}
		return out
	case *Entity:
		return entityPerceptionMap(v, w, agentID)
	default:
		return result
	}
}

// entityPerceptionMap projects one entity into the perception format:
// { "id": "...", "type": "...", "resources": { public resources } }
// The agent's own entity gets all resources (no visibility filter).
func entityPerceptionMap(e *Entity, w *World, agentID string) map[string]any {
	m := map[string]any{
		"id":   e.id,
		"type": e.typeName,
	}
	if e.id == agentID {
		m["resources"] = w.resources.AllResources(e.id)
	} else {
		pub := publicResources(w, e)
		if len(pub) > 0 {
			m["resources"] = pub
		}
	}
	return m
}

// publicResources returns the agent-visible (Public) resources for entity e.
func publicResources(w *World, e *Entity) map[string]any {
	all := w.resources.AllResources(e.id)
	out := make(map[string]any)
	for k, v := range all {
		vis := w.registry.Visibility(e.typeName, k)
		if vis == VisibilityPublic {
			out[k] = v
		}
	}
	return out
}

// BuildAvailableActions returns the available_actions list for the agent in the
// spec §9.2 format: [{name, params}]. Includes actions on the agent's own type,
// actions on the agent's current location type, and the built-in "move" action.
func BuildAvailableActions(w *World, agentID string) []map[string]any {
	e, ok := w.entities[agentID]
	if !ok {
		return nil
	}
	seen := make(map[string]bool)

	// Own type actions.
	if td, err := w.registry.Get(e.typeName); err == nil {
		for name := range td.actions {
			seen[name] = true
		}
	}

	// Location type actions (agent can invoke these on its container).
	if locID := w.graph.LocationOf(agentID); locID != "" {
		if loc, ok := w.entities[locID]; ok {
			if ltd, err := w.registry.Get(loc.typeName); err == nil {
				for name := range ltd.actions {
					seen[name] = true
				}
			}
		}
	}

	// Built-in move action.
	seen["move"] = true

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sortStrings(names)

	out := make([]map[string]any, 0, len(names))
	for _, name := range names {
		out = append(out, map[string]any{"name": name})
	}
	return out
}
