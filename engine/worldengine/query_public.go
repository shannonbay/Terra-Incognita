package worldengine

// This file exposes the query language as the public API on World and Entity.
// The stubs in world.go and entity.go delegate here.

// QueryResult executes a query expression against the world and returns the raw result.
// For scoring functions, tick functions, and MCP tools that need untyped results.
func (w *World) QueryResult(expr string) (any, error) {
	ast, err := ParseQuery(expr)
	if err != nil {
		return nil, err
	}
	ex := newQueryExecutor(w)
	return ex.Execute(ast, WorldQueryContext), nil
}

// QueryEntities executes a query and returns the matched entities.
// Returns nil if the query produces no entity set.
func (w *World) QueryEntities(expr string) []*Entity {
	result, err := w.QueryResult(expr)
	if err != nil {
		return nil
	}
	return toEntities(result)
}

// QueryAgentPerception executes a query scoped to an agent's visibility.
// Hidden and private resources on other entities are stripped.
func (w *World) QueryAgentPerception(expr string, agentID string) (any, error) {
	ast, err := ParseQuery(expr)
	if err != nil {
		return nil, err
	}
	ex := newQueryExecutor(w)
	ctx := QueryContext{AgentID: agentID, SelfID: agentID}
	return ex.Execute(ast, ctx), nil
}

// Query implements the World.Query stub — returns any for use in scoring/tick.
func (w *World) query(expr string) any {
	result, _ := w.QueryResult(expr)
	return result
}

// QueryFloat implements World.QueryFloat — returns a float64 scalar.
func (w *World) queryFloat(expr string) float64 {
	result, _ := w.QueryResult(expr)
	return toFloat(result)
}

// entityQuery is called from Entity.Query — scopes to the entity's visibility.
func (w *World) entityQuery(entityID, expr string) any {
	ast, err := ParseQuery(expr)
	if err != nil {
		return nil
	}
	ex := newQueryExecutor(w)
	ctx := QueryContext{SelfID: entityID}
	return ex.Execute(ast, ctx)
}
