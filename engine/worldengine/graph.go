package worldengine

// Graph is the connection graph and containment hierarchy for the world.
// It stores:
//   - typed weighted edges between entities (the "connection graph")
//   - containment relationships (which entity is "inside" which)
//
// Implemented in full in Phase 3. This file provides the interface that
// Entity and World depend on so Phases 1–2 compile.
type Graph struct {
	// edges: fromID → list of outgoing connections
	edges map[string][]Connection

	// reverseEdges: toID → list of incoming connections (for bidirectional lookup)
	reverseEdges map[string][]Connection

	// location: childID → parentID (containment)
	location map[string]string

	// children: parentID → set of childIDs
	children map[string]map[string]bool

	// typeIndex: connType → set of (from, to) pairs for fast type filtering
	typeIndex map[string][]Connection
}

func newGraph() *Graph {
	return &Graph{
		edges:        make(map[string][]Connection),
		reverseEdges: make(map[string][]Connection),
		location:     make(map[string]string),
		children:     make(map[string]map[string]bool),
		typeIndex:    make(map[string][]Connection),
	}
}

// AddEdge inserts a connection. If directed is false, adds edges in both directions.
func (g *Graph) AddEdge(fromID, toID, connType string, weight float64, directed bool) {
	fwd := Connection{From: fromID, To: toID, Type: connType, Weight: weight, Directed: directed}
	g.edges[fromID] = append(g.edges[fromID], fwd)
	g.typeIndex[connType] = append(g.typeIndex[connType], fwd)

	if !directed {
		rev := Connection{From: toID, To: fromID, Type: connType, Weight: weight, Directed: false}
		g.edges[toID] = append(g.edges[toID], rev)
		g.reverseEdges[fromID] = append(g.reverseEdges[fromID], rev)
	}
}

// RemoveEdge deletes connections matching (fromID, toID, connType).
// If the original edge was bidirectional, also removes the reverse.
func (g *Graph) RemoveEdge(fromID, toID, connType string) {
	g.edges[fromID] = filterConns(g.edges[fromID], toID, connType)
	g.edges[toID] = filterConns(g.edges[toID], fromID, connType)
	g.typeIndex[connType] = filterConnsFrom(g.typeIndex[connType], fromID, toID)
}

func filterConns(conns []Connection, toID, connType string) []Connection {
	out := conns[:0]
	for _, c := range conns {
		if c.To == toID && c.Type == connType {
			continue
		}
		out = append(out, c)
	}
	return out
}

func filterConnsFrom(conns []Connection, fromID, toID string) []Connection {
	out := conns[:0]
	for _, c := range conns {
		if (c.From == fromID && c.To == toID) || (c.From == toID && c.To == fromID) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// ConnectionsFrom returns all outgoing connections from fromID.
// AllEdges returns all edges in the graph. For bidirectional edges, only the
// canonical direction (From < To lexicographically, or first-inserted) is returned
// by iterating the forward edges map and deduplicating by (From,To,Type).
func (g *Graph) AllEdges() []Connection {
	seen := make(map[[3]string]bool)
	var out []Connection
	// Collect all from-edges in deterministic order.
	froms := make([]string, 0, len(g.edges))
	for f := range g.edges {
		froms = append(froms, f)
	}
	sortStrings(froms)
	for _, from := range froms {
		for _, c := range g.edges[from] {
			key := [3]string{c.From, c.To, c.Type}
			if !seen[key] {
				seen[key] = true
				out = append(out, c)
			}
		}
	}
	return out
}

func (g *Graph) ConnectionsFrom(fromID string) []Connection {
	return g.edges[fromID]
}

// NeighborsOfType returns the IDs of entities reachable from fromID via connType.
func (g *Graph) NeighborsOfType(fromID, connType string) []string {
	var out []string
	for _, c := range g.edges[fromID] {
		if c.Type == connType {
			out = append(out, c.To)
		}
	}
	return out
}

// HasEdge reports whether a connection of connType exists from fromID to toID.
func (g *Graph) HasEdge(fromID, toID, connType string) bool {
	for _, c := range g.edges[fromID] {
		if c.To == toID && c.Type == connType {
			return true
		}
	}
	return false
}

// ConnectionsOfType returns all connections of a given type.
func (g *Graph) ConnectionsOfType(connType string) []Connection {
	return g.typeIndex[connType]
}

// Place sets childID's location to parentID.
func (g *Graph) Place(childID, parentID string) {
	// Remove from old container if any
	if old, ok := g.location[childID]; ok {
		if g.children[old] != nil {
			delete(g.children[old], childID)
		}
	}
	g.location[childID] = parentID
	if g.children[parentID] == nil {
		g.children[parentID] = make(map[string]bool)
	}
	g.children[parentID][childID] = true
}

// Unplace removes childID from its container.
func (g *Graph) Unplace(childID string) {
	if old, ok := g.location[childID]; ok {
		if g.children[old] != nil {
			delete(g.children[old], childID)
		}
		delete(g.location, childID)
	}
}

// LocationOf returns the container of childID, or "" if none.
func (g *Graph) LocationOf(childID string) string {
	return g.location[childID]
}

// ContainedBy returns all entities directly inside parentID (sorted for determinism).
func (g *Graph) ContainedBy(parentID string) []string {
	kids := g.children[parentID]
	out := make([]string, 0, len(kids))
	for id := range kids {
		out = append(out, id)
	}
	sortStrings(out)
	return out
}

// RemoveEntity removes all edges and containment references for entityID.
func (g *Graph) RemoveEntity(entityID string) {
	// Remove as child from container
	g.Unplace(entityID)

	// Remove all outgoing edges
	for _, conn := range g.edges[entityID] {
		g.edges[conn.To] = filterConns(g.edges[conn.To], entityID, conn.Type)
	}
	delete(g.edges, entityID)
	delete(g.reverseEdges, entityID)

	// Remove all contained children (they become uncontained)
	for childID := range g.children[entityID] {
		delete(g.location, childID)
	}
	delete(g.children, entityID)

	// Rebuild type index (rare operation — acceptable O(n))
	for connType, conns := range g.typeIndex {
		var filtered []Connection
		for _, c := range conns {
			if c.From != entityID && c.To != entityID {
				filtered = append(filtered, c)
			}
		}
		g.typeIndex[connType] = filtered
	}
}

// Snapshot returns a deep copy of the graph state.
func (g *Graph) Snapshot() *GraphSnapshot {
	snap := &GraphSnapshot{
		edges:    make(map[string][]Connection, len(g.edges)),
		location: make(map[string]string, len(g.location)),
		children: make(map[string]map[string]bool, len(g.children)),
	}
	for id, conns := range g.edges {
		c := make([]Connection, len(conns))
		copy(c, conns)
		snap.edges[id] = c
	}
	for k, v := range g.location {
		snap.location[k] = v
	}
	for k, v := range g.children {
		m := make(map[string]bool, len(v))
		for id, b := range v {
			m[id] = b
		}
		snap.children[k] = m
	}
	return snap
}

// GraphSnapshot is a point-in-time copy of the graph used by the state reconstructor.
type GraphSnapshot struct {
	edges    map[string][]Connection
	location map[string]string
	children map[string]map[string]bool
}

// Restore replaces the graph state with a snapshot.
func (g *Graph) Restore(snap *GraphSnapshot) {
	g.edges = make(map[string][]Connection, len(snap.edges))
	for id, conns := range snap.edges {
		c := make([]Connection, len(conns))
		copy(c, conns)
		g.edges[id] = c
	}
	g.location = make(map[string]string, len(snap.location))
	for k, v := range snap.location {
		g.location[k] = v
	}
	g.children = make(map[string]map[string]bool, len(snap.children))
	for k, v := range snap.children {
		m := make(map[string]bool, len(v))
		for id, b := range v {
			m[id] = b
		}
		g.children[k] = m
	}
	// Rebuild type index from edges
	g.typeIndex = make(map[string][]Connection)
	for _, conns := range g.edges {
		for _, c := range conns {
			g.typeIndex[c.Type] = append(g.typeIndex[c.Type], c)
		}
	}
}
