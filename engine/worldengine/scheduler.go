package worldengine

// TickScheduler executes all entity tick functions each simulation step.
//
// Execution order is deterministic: entities run in lexicographic order by ID.
// This guarantees reproducible simulations given the same world definition and
// random seed.
//
// Parallelism (spec §7.4): entities whose tick functions don't share any
// property tables can run concurrently. For the current implementation we run
// all ticks sequentially and leave parallel scheduling as a future optimisation.
// The API is designed so the parallel version is a drop-in replacement.
type TickScheduler struct {
	world *World
}

func newTickScheduler(w *World) *TickScheduler {
	return &TickScheduler{world: w}
}

// ExecuteTicks runs every entity's tick function in deterministic order.
// Actions queued during tick execution accumulate in w.pendingActions.
func (s *TickScheduler) ExecuteTicks(dt float64) {
	w := s.world

	// Collect and sort entity IDs for determinism.
	ids := make([]string, 0, len(w.entities))
	for id := range w.entities {
		ids = append(ids, id)
	}
	sortStrings(ids)

	for _, id := range ids {
		e := w.entities[id]
		fn := w.registry.TickFn(e.typeName)
		if fn != nil {
			fn(e, dt)
		}
	}
}
