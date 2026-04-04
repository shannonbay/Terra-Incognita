package worldengine

// TransitionManager processes built-in "move" actions.
//
// For each move action:
//  1. Validate the target is reachable — the invoker's current location must be
//     connected to the target, OR the invoker itself must be connected (for
//     top-level entities).
//  2. Apply the world's MovementCost function (if registered). If it returns
//     false the move fails silently.
//  3. Update the containment map: remove invoker from old container, place in new.
//
// If a type defines its own "move" action, the engine uses that handler instead
// (handled by ActionDispatcher — the type's action takes precedence over the
// built-in because it is registered on the TypeDef).
type TransitionManager struct {
	world *World
}

func newTransitionManager(w *World) *TransitionManager {
	return &TransitionManager{world: w}
}

// movedEntity records the result of a completed move for event logging.
type movedEntity struct {
	entityID  string
	fromLocID string
	toLocID   string
	conn      Connection
}

// Process applies all queued move actions, returning a list of completed moves.
func (tm *TransitionManager) Process(moves []pendingAction) []movedEntity {
	w := tm.world
	var completed []movedEntity

	for _, a := range moves {
		invoker, ok := w.entities[a.invokerID]
		if !ok {
			continue
		}

		targetID, ok := a.params["target"].(string)
		if !ok || targetID == "" {
			continue
		}

		// Validate target entity exists.
		if _, ok := w.entities[targetID]; !ok {
			continue
		}

		// Find the connection from invoker's current container (or from invoker itself)
		// to the target.
		conn, found := tm.findConnection(invoker, targetID)
		if !found {
			continue // target not reachable
		}

		// Apply movement cost if registered.
		if w.moveCostFn != nil {
			if !w.moveCostFn(invoker, conn) {
				continue // cost function denied the move
			}
		}

		fromLoc := w.graph.LocationOf(invoker.id)

		// Update containment.
		w.graph.Place(invoker.id, targetID)

		completed = append(completed, movedEntity{
			entityID:  invoker.id,
			fromLocID: fromLoc,
			toLocID:   targetID,
			conn:      conn,
		})
	}
	return completed
}

// findConnection looks for a connection from:
//   - invoker's current location → targetID  (most common: a boat moves between lakes)
//   - invoker itself → targetID  (top-level entity with its own connections)
func (tm *TransitionManager) findConnection(invoker *Entity, targetID string) (Connection, bool) {
	w := tm.world

	searchFrom := w.graph.LocationOf(invoker.id)
	if searchFrom == "" {
		searchFrom = invoker.id
	}

	for _, c := range w.graph.ConnectionsFrom(searchFrom) {
		if c.To == targetID {
			return c, true
		}
	}
	return Connection{}, false
}
