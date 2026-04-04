package worldengine

// BuildDiscoveryPerception returns the always-available discovery perception
// block for an agent (spec §9.7). These are added regardless of what is in
// the agent's configured Perception list.
//
// | Query                        | Returns                                     |
// |------------------------------|---------------------------------------------|
// | /self                        | Agent's own resource map                    |
// | /self/available_actions      | []string of action names                    |
// | /self/location               | Container entity (id, type, public resources)|
// | /self/location/contains      | Peer entities at same location               |
// | /world/config                | World config: MaxTicks, DT, CurrentTick...   |
func BuildDiscoveryPerception(w *World, agentID string) map[string]any {
	disc := make(map[string]any)

	e, ok := w.entities[agentID]
	if !ok {
		return disc
	}

	// /self — own resources (no visibility filter)
	disc["/self"] = w.resources.AllResources(e.id)

	// /self/available_actions
	disc["/self/available_actions"] = BuildAvailableActions(w, agentID)

	// /self/location
	locID := w.graph.LocationOf(agentID)
	if locID != "" {
		if loc, ok := w.entities[locID]; ok {
			disc["/self/location"] = entityPerceptionMap(loc, w, agentID)
		}
	}

	// /self/location/contains — peers at same location (excluding self)
	if locID != "" {
		peers := w.graph.ContainedBy(locID)
		var peerList []map[string]any
		for _, pid := range peers {
			if pid == agentID {
				continue
			}
			if pe, ok := w.entities[pid]; ok {
				peerList = append(peerList, entityPerceptionMap(pe, w, agentID))
			}
		}
		disc["/self/location/contains"] = peerList
	}

	// /world/config
	disc["/world/config"] = map[string]any{
		"MaxTicks":          w.config.MaxTicks,
		"MaxActionsPerTick": w.config.MaxActionsPerTick,
		"TickUnit":          w.config.TickUnit,
		"DT":                w.config.DT,
		"CurrentTick":       w.currentTick,
	}

	// /self/connections — direct connections with optional description
	conns := w.graph.ConnectionsFrom(agentID)
	if len(conns) > 0 {
		var connList []map[string]any
		for _, c := range conns {
			cm := map[string]any{
				"to":     c.To,
				"type":   c.Type,
				"weight": c.Weight,
			}
			if w.connDescFn != nil {
				cm["description"] = w.connDescFn(c)
			}
			connList = append(connList, cm)
		}
		disc["/self/connections"] = connList
	}

	return disc
}
