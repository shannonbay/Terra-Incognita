package worldengine

import "fmt"

// ActionDispatcher merges and executes all pending actions collected during
// a tick. It enforces:
//   - MaxActionsPerTick limit per entity
//   - Local/remote colocation rules
//   - Target resolution (explicit "target" param → entity ID; default → location or self)
//
// Actions are dispatched in deterministic order (sorted by invokerID, then
// by position in the queue — preserving the order each entity queued them).
type ActionDispatcher struct {
	world *World
}

func newActionDispatcher(w *World) *ActionDispatcher {
	return &ActionDispatcher{world: w}
}

// dispatchedAction is an action enriched with resolved target/invoker entities.
type dispatchedAction struct {
	pending  pendingAction
	invoker  *Entity
	target   *Entity
	result   ActionResult
	resolved bool
}

// Dispatch executes the given actions, returning their results.
// moveActions are separated out and returned for the TransitionManager to handle.
func (d *ActionDispatcher) Dispatch(actions []pendingAction) (results []dispatchedAction, moveActions []pendingAction) {
	w := d.world
	maxPerTick := w.config.MaxActionsPerTick

	// Count actions per invoker to enforce MaxActionsPerTick.
	invokerCount := make(map[string]int)

	for _, a := range actions {
		if maxPerTick > 0 && invokerCount[a.invokerID] >= maxPerTick {
			continue // silently drop over-limit actions
		}
		invokerCount[a.invokerID]++

		// move is handled by TransitionManager, not here
		if a.name == "move" {
			moveActions = append(moveActions, a)
			continue
		}

		da := dispatchedAction{pending: a}

		invoker, ok := w.entities[a.invokerID]
		if !ok {
			continue // invoker was destroyed mid-tick
		}
		da.invoker = invoker

		// Resolve target entity.
		target := d.resolveTarget(a, invoker)
		if target == nil {
			// No valid target — skip
			continue
		}
		da.target = target

		// Look up handler on target's type.
		handler, ok := w.registry.ActionHandler(target.typeName, a.name)
		if !ok {
			continue // action not defined on target's type
		}

		// Colocation check for local actions.
		if !w.registry.IsRemoteAction(target.typeName, a.name) {
			if !d.colocated(invoker, target) {
				da.result = Fail(fmt.Sprintf("invoker %q is not colocated with target %q", invoker.id, target.id))
				da.resolved = true
				results = append(results, da)
				continue
			}
		}

		// Execute.
		da.result = handler(target, invoker, a.params)
		da.resolved = true
		results = append(results, da)
	}
	return
}

// resolveTarget determines the target entity for an action.
// Priority: explicit "target" param → invoker's location → invoker itself.
func (d *ActionDispatcher) resolveTarget(a pendingAction, invoker *Entity) *Entity {
	w := d.world

	if targetID, ok := a.params["target"]; ok {
		id, ok := targetID.(string)
		if !ok {
			return nil
		}
		return w.entities[id]
	}

	// Default: try invoker's location first, then self.
	if loc := invoker.Location(); loc != nil {
		// Check if the action is defined on the location's type.
		if _, ok := w.registry.ActionHandler(loc.typeName, a.name); ok {
			return loc
		}
	}

	// Self-targeted.
	return invoker
}

// colocated reports whether invoker and target satisfy the local action constraint.
// The invoker must be inside the target OR the target must be inside the invoker.
func (d *ActionDispatcher) colocated(invoker, target *Entity) bool {
	w := d.world
	invLoc := w.graph.LocationOf(invoker.id)
	return invLoc == target.id || w.graph.LocationOf(target.id) == invoker.id
}
