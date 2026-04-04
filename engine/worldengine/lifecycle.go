package worldengine

// LifecycleManager processes entity creation and destruction queued during a tick.
//
// Deferred application ensures that:
//   - Newly spawned entities are not visible to tick functions in the same tick.
//   - Entities destroyed during the tick still participate in action resolution
//     for that tick (their handlers can still execute).
//   - The entity map is only mutated once per tick, in a controlled phase.
type LifecycleManager struct {
	world *World
}

func newLifecycleManager(w *World) *LifecycleManager {
	return &LifecycleManager{world: w}
}

// ApplySpawns creates all queued entities. Returns the IDs of spawned entities.
func (lm *LifecycleManager) ApplySpawns(spawns []pendingSpawn) []string {
	w := lm.world
	var ids []string
	for _, s := range spawns {
		if _, exists := w.entities[s.id]; exists {
			continue // silently skip duplicates
		}
		td, err := w.registry.Get(s.typeName)
		if err != nil {
			continue // unknown type
		}
		_ = td

		e := &Entity{id: s.id, typeName: s.typeName, world: w}
		w.entities[s.id] = e

		defaultRes := w.registry.DefaultResources(s.typeName)
		defaultPar := w.registry.DefaultParams(s.typeName)
		initRes := s.init.Resources
		initPar := s.init.Params
		if initRes == nil {
			initRes = P{}
		}
		if initPar == nil {
			initPar = P{}
		}
		w.resources.InitEntity(s.id, defaultRes, initRes, defaultPar, initPar)

		if s.init.Location != "" {
			w.graph.Place(s.id, s.init.Location)
		}
		ids = append(ids, s.id)
	}
	return ids
}

// ApplyDestroys removes all queued entities. Returns the IDs of destroyed entities.
func (lm *LifecycleManager) ApplyDestroys(destroys []pendingDestroy) []string {
	w := lm.world
	var ids []string
	seen := make(map[string]bool)
	for _, d := range destroys {
		if seen[d.id] {
			continue
		}
		seen[d.id] = true
		if _, ok := w.entities[d.id]; !ok {
			continue
		}
		delete(w.entities, d.id)
		w.resources.RemoveEntity(d.id)
		w.graph.RemoveEntity(d.id)
		ids = append(ids, d.id)
	}
	return ids
}
