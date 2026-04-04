package worldengine

import (
	"math"
	"strconv"
)

// ----------------------------------------------------------------------------
// Query context
// ----------------------------------------------------------------------------

// QueryContext controls visibility enforcement during query execution.
type QueryContext struct {
	// AgentID is set for agent-scoped queries (perception system).
	// When non-empty, hidden and private resources are stripped from other entities.
	AgentID string

	// SelfID is the entity the query is scoped to (used for /self, /location paths).
	SelfID string
}

// WorldQueryContext returns a context for world-level queries (no visibility filtering).
var WorldQueryContext = QueryContext{}

// ----------------------------------------------------------------------------
// Executor
// ----------------------------------------------------------------------------

// QueryExecutor walks a parsed QueryAST against the world's live state.
type QueryExecutor struct {
	world *World
}

func newQueryExecutor(w *World) *QueryExecutor {
	return &QueryExecutor{world: w}
}

// Execute runs the query and returns the result.
// Returns:
//   - []*Entity for entity-set results
//   - map[string]any for property-map results
//   - float64 for aggregated numeric results
//   - int for @count
//   - []string for @available_actions
//   - nil for empty / no match
func (ex *QueryExecutor) Execute(ast *QueryAST, ctx QueryContext) any {
	// Start with the world's entity set or a specific entity depending on first segment.
	var current any = ex.allEntities()

	for _, seg := range ast.Segments {
		current = ex.applySegment(current, seg, ctx)
		if current == nil {
			return nil
		}
	}

	if ast.Aggregator != "" {
		return ex.aggregate(current, ast.Aggregator)
	}
	return current
}

// allEntities returns all entities as a slice, sorted for determinism.
func (ex *QueryExecutor) allEntities() []*Entity {
	ids := make([]string, 0, len(ex.world.entities))
	for id := range ex.world.entities {
		ids = append(ids, id)
	}
	sortStrings(ids)
	out := make([]*Entity, 0, len(ids))
	for _, id := range ids {
		out = append(out, ex.world.entities[id])
	}
	return out
}

// applySegment applies one path step to the current result.
func (ex *QueryExecutor) applySegment(current any, seg Segment, ctx QueryContext) any {
	switch s := seg.(type) {

	case SegEntities:
		entities := ex.allEntities()
		// Filter by ID if specific
		if s.ID != "" && s.ID != "*" {
			e, ok := ex.world.entities[s.ID]
			if !ok {
				return nil
			}
			entities = []*Entity{e}
		}
		// Apply predicates
		return ex.filterEntities(entities, s.Preds, ctx)

	case SegSelf:
		if ctx.SelfID == "" {
			return nil
		}
		e, ok := ex.world.entities[ctx.SelfID]
		if !ok {
			return nil
		}
		return []*Entity{e}

	case SegLocation:
		entities := toEntities(current)
		var out []*Entity
		seen := map[string]bool{}
		for _, e := range entities {
			locID := ex.world.graph.LocationOf(e.id)
			if locID == "" || seen[locID] {
				continue
			}
			seen[locID] = true
			if loc, ok := ex.world.entities[locID]; ok {
				out = append(out, loc)
			}
		}
		return out

	case SegContains:
		entities := toEntities(current)
		var out []*Entity
		seen := map[string]bool{}
		for _, e := range entities {
			for _, childID := range ex.world.graph.ContainedBy(e.id) {
				if seen[childID] {
					continue
				}
				seen[childID] = true
				if child, ok := ex.world.entities[childID]; ok {
					out = append(out, child)
				}
			}
		}
		return ex.filterEntities(out, s.Preds, ctx)

	case SegNeighbors:
		entities := toEntities(current)
		return ex.traverseNeighbors(entities, s, ctx)

	case SegResources:
		entities := toEntities(current)
		return ex.projectResources(entities, s.Field, ctx)

	case SegParams:
		entities := toEntities(current)
		return ex.projectParams(entities, s.Field)

	case SegAvailableActions:
		entities := toEntities(current)
		return ex.availableActions(entities)

	case SegWorldConfig:
		return map[string]any{
			"MaxTicks":          ex.world.config.MaxTicks,
			"MaxActionsPerTick": ex.world.config.MaxActionsPerTick,
			"TickUnit":          ex.world.config.TickUnit,
			"DT":                ex.world.config.DT,
			"CurrentTick":       ex.world.currentTick,
		}
	}
	return current
}

// ----------------------------------------------------------------------------
// Traversal helpers
// ----------------------------------------------------------------------------

func (ex *QueryExecutor) traverseNeighbors(entities []*Entity, s SegNeighbors, ctx QueryContext) []*Entity {
	depth := s.Depth
	if depth <= 0 {
		depth = 1
	}

	frontier := entities
	seen := map[string]bool{}
	for _, e := range entities {
		seen[e.id] = true
	}

	var out []*Entity
	for d := 0; d < depth; d++ {
		var next []*Entity
		for _, e := range frontier {
			conns := ex.world.graph.ConnectionsFrom(e.id)
			for _, conn := range conns {
				if s.ConnType != "" && conn.Type != s.ConnType {
					continue
				}
				if seen[conn.To] {
					continue
				}
				seen[conn.To] = true
				if neighbor, ok := ex.world.entities[conn.To]; ok {
					next = append(next, neighbor)
				}
			}
		}
		out = append(out, next...)
		frontier = next
	}
	return ex.filterEntities(out, s.Preds, ctx)
}

// ----------------------------------------------------------------------------
// Predicate filtering
// ----------------------------------------------------------------------------

func (ex *QueryExecutor) filterEntities(entities []*Entity, preds []Predicate, ctx QueryContext) []*Entity {
	if len(preds) == 0 {
		return entities
	}
	var out []*Entity
	for _, e := range entities {
		if ex.matchAll(e, preds, ctx) {
			out = append(out, e)
		}
	}
	return out
}

func (ex *QueryExecutor) matchAll(e *Entity, preds []Predicate, ctx QueryContext) bool {
	for _, p := range preds {
		if !ex.matchPred(e, p, ctx) {
			return false
		}
	}
	return true
}

func (ex *QueryExecutor) matchPred(e *Entity, pred Predicate, ctx QueryContext) bool {
	switch pred.Key {
	case "type":
		return compareStr(e.typeName, pred.Op, pred.Value)

	default:
		// "resources.fuel", "params.speed", or bare field name
		var val float64
		var ok bool

		if len(pred.Key) > 10 && pred.Key[:10] == "resources." {
			field := pred.Key[10:]
			var rawVal any
			rawVal, ok = ex.world.resources.GetResource(e.id, field)
			if !ok {
				return false
			}
			val = toFloat(rawVal)
		} else if len(pred.Key) > 7 && pred.Key[:7] == "params." {
			field := pred.Key[7:]
			raw := ex.world.resources.GetParam(e.id, field)
			val = toFloat(raw)
			ok = true
		} else {
			// Try as resource first, then param
			raw, exists := ex.world.resources.GetResource(e.id, pred.Key)
			if exists {
				val = toFloat(raw)
				ok = true
			}
		}

		if !ok {
			return false
		}

		if pf, isNum := pred.ValueFloat(); isNum {
			return compareFloat(val, pred.Op, pf)
		}
		return compareStr(strconv.FormatFloat(val, 'f', -1, 64), pred.Op, pred.Value)
	}
}

func compareStr(a, op, b string) bool {
	switch op {
	case "=":
		return a == b
	case ">":
		return a > b
	case "<":
		return a < b
	}
	return false
}

func compareFloat(a float64, op string, b float64) bool {
	switch op {
	case "=":
		return a == b
	case ">":
		return a > b
	case "<":
		return a < b
	case ">=":
		return a >= b
	case "<=":
		return a <= b
	}
	return false
}

// ----------------------------------------------------------------------------
// Projection
// ----------------------------------------------------------------------------

func (ex *QueryExecutor) projectResources(entities []*Entity, field string, ctx QueryContext) any {
	if len(entities) == 1 && field != "" && field != "*" {
		e := entities[0]
		// Visibility check for agent queries
		if ctx.AgentID != "" && ctx.AgentID != e.id {
			vis := ex.world.registry.Visibility(e.typeName, field)
			if vis != VisibilityPublic {
				return nil
			}
		}
		v, ok := ex.world.resources.GetResource(e.id, field)
		if !ok {
			return nil
		}
		return v
	}

	// Multiple entities or wildcard field: return []map
	var out []map[string]any
	for _, e := range entities {
		res := ex.world.resources.AllResources(e.id)
		// Enforce visibility for agent queries
		if ctx.AgentID != "" && ctx.AgentID != e.id {
			filtered := make(map[string]any)
			for k, v := range res {
				vis := ex.world.registry.Visibility(e.typeName, k)
				if vis == VisibilityPublic {
					filtered[k] = v
				}
			}
			res = filtered
		}
		if field != "" && field != "*" {
			v, ok := res[field]
			if ok {
				out = append(out, map[string]any{"id": e.id, field: v})
			}
		} else {
			res["id"] = e.id
			out = append(out, res)
		}
	}
	if len(out) == 1 {
		return out[0]
	}
	return out
}

func (ex *QueryExecutor) projectParams(entities []*Entity, field string) any {
	if len(entities) == 1 && field != "" && field != "*" {
		e := entities[0]
		v := ex.world.resources.GetParam(e.id, field)
		return v
	}
	var out []map[string]any
	for _, e := range entities {
		params := ex.world.resources.AllParams(e.id)
		if field != "" && field != "*" {
			v, ok := params[field]
			if ok {
				out = append(out, map[string]any{"id": e.id, field: v})
			}
		} else {
			params["id"] = e.id
			out = append(out, params)
		}
	}
	return out
}

func (ex *QueryExecutor) availableActions(entities []*Entity) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range entities {
		td, err := ex.world.registry.Get(e.typeName)
		if err != nil {
			continue
		}
		for name := range td.actions {
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	sortStrings(out)
	return out
}

// ----------------------------------------------------------------------------
// Aggregation
// ----------------------------------------------------------------------------

func (ex *QueryExecutor) aggregate(current any, op string) any {
	if op == "count" {
		return ex.countResult(current)
	}

	// Collect numeric values from entities or scalar collections
	values := ex.collectFloats(current)
	if len(values) == 0 {
		return 0.0
	}

	switch op {
	case "sum":
		var total float64
		for _, v := range values {
			total += v
		}
		return total
	case "min":
		m := math.Inf(1)
		for _, v := range values {
			if v < m {
				m = v
			}
		}
		return m
	case "max":
		m := math.Inf(-1)
		for _, v := range values {
			if v > m {
				m = v
			}
		}
		return m
	case "avg":
		var total float64
		for _, v := range values {
			total += v
		}
		return total / float64(len(values))
	}
	return nil
}

func (ex *QueryExecutor) countResult(current any) int {
	switch v := current.(type) {
	case []*Entity:
		return len(v)
	case []any:
		return len(v)
	case []map[string]any:
		return len(v)
	case nil:
		return 0
	default:
		return 1
	}
}

func (ex *QueryExecutor) collectFloats(current any) []float64 {
	switch v := current.(type) {
	case []*Entity:
		// Should have been projected to a resource already; return empty
		return nil
	case []map[string]any:
		var out []float64
		for _, m := range v {
			for _, val := range m {
				if id, ok := val.(string); ok && id == m["id"] {
					continue
				}
				out = append(out, toFloat(val))
			}
		}
		return out
	case float64:
		return []float64{v}
	case int:
		return []float64{float64(v)}
	case []any:
		var out []float64
		for _, item := range v {
			out = append(out, toFloat(item))
		}
		return out
	}
	return nil
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

func toEntities(current any) []*Entity {
	switch v := current.(type) {
	case []*Entity:
		return v
	case *Entity:
		return []*Entity{v}
	}
	return nil
}

func toFloat(v any) float64 {
	switch f := v.(type) {
	case float64:
		return f
	case int:
		return float64(f)
	case int64:
		return float64(f)
	case float32:
		return float64(f)
	}
	return 0
}
