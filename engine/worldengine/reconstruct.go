package worldengine

import (
	"database/sql"
	"encoding/json"
)

// ---------------------------------------------------------------------------
// WorldState — read-only reconstructed simulation state
// ---------------------------------------------------------------------------

// WorldState is a read-only snapshot of the world at a specific tick.
// It is produced by StateReconstructor.StateAt and is not a live World.
type WorldState struct {
	Tick        int
	Entities    map[string]*EntityState
	Connections []ConnectionState
}

// EntityState holds the complete state of one entity at a reconstructed tick.
type EntityState struct {
	ID        string
	TypeName  string
	Resources map[string]any
	Params    map[string]any
	Location  string
}

// ConnectionState holds one edge in the reconstructed connection graph.
type ConnectionState struct {
	From, To, Type string
	Weight         float64
	Directed       bool
}

// Get returns a resource value, or 0 if absent.
func (es *EntityState) Get(resource string) float64 {
	v, ok := es.Resources[resource]
	if !ok {
		return 0
	}
	return toFloat(v)
}

// Entity returns the EntityState for id, or nil if not found.
func (ws *WorldState) Entity(id string) *EntityState {
	return ws.Entities[id]
}

// ---------------------------------------------------------------------------
// StateReconstructor
// ---------------------------------------------------------------------------

// StateReconstructor rebuilds WorldState at any tick from the run log.
type StateReconstructor struct{}

// StateAt finds the nearest snapshot ≤ tick, then replays delta events forward.
func (r *StateReconstructor) StateAt(db *sql.DB, tick int) (*WorldState, error) {
	// Load nearest snapshot at or before tick.
	snapTick, entitiesJSON, connectionsJSON, err := loadNearestSnapshot(db, tick)
	if err != nil {
		return nil, err
	}

	ws := &WorldState{
		Tick:     tick,
		Entities: make(map[string]*EntityState),
	}

	// Decode entity snapshot.
	var entMap map[string]struct {
		Type      string         `json:"type"`
		Params    map[string]any `json:"params"`
		Resources map[string]any `json:"resources"`
		Location  string         `json:"location"`
	}
	if err := json.Unmarshal([]byte(entitiesJSON), &entMap); err != nil {
		return nil, err
	}
	for id, es := range entMap {
		ws.Entities[id] = &EntityState{
			ID:        id,
			TypeName:  es.Type,
			Resources: es.Resources,
			Params:    es.Params,
			Location:  es.Location,
		}
	}

	// Decode connection snapshot.
	var conns []struct {
		From     string  `json:"from"`
		To       string  `json:"to"`
		Type     string  `json:"type"`
		Weight   float64 `json:"weight"`
		Directed bool    `json:"directed"`
	}
	if err := json.Unmarshal([]byte(connectionsJSON), &conns); err != nil {
		return nil, err
	}
	for _, c := range conns {
		ws.Connections = append(ws.Connections, ConnectionState{
			From: c.From, To: c.To, Type: c.Type, Weight: c.Weight, Directed: c.Directed,
		})
	}

	// Replay events from (snapTick + 1) through tick.
	if tick > snapTick {
		if err := r.replayEvents(db, ws, snapTick+1, tick); err != nil {
			return nil, err
		}
	}
	return ws, nil
}

func loadNearestSnapshot(db *sql.DB, tick int) (snapTick int, entitiesJSON, connectionsJSON string, err error) {
	row := db.QueryRow(
		`SELECT tick, entities, connections FROM snapshots WHERE tick <= ? ORDER BY tick DESC LIMIT 1`,
		tick,
	)
	err = row.Scan(&snapTick, &entitiesJSON, &connectionsJSON)
	if err == sql.ErrNoRows {
		// No snapshot at all — return empty state at tick -1.
		return -1, `{}`, `[]`, nil
	}
	return
}

func (r *StateReconstructor) replayEvents(db *sql.DB, ws *WorldState, fromTick, toTick int) error {
	rows, err := db.Query(
		`SELECT event_type, entity_id, field, new_value, meta FROM events WHERE tick >= ? AND tick <= ? ORDER BY id`,
		fromTick, toTick,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var evType, entityID string
		var field, newValue, meta sql.NullString
		if err := rows.Scan(&evType, &entityID, &field, &newValue, &meta); err != nil {
			return err
		}
		switch evType {
		case "resource_set":
			es := ws.ensureEntity(entityID)
			if field.Valid && field.String != "" && newValue.Valid {
				var v any
				if err := json.Unmarshal([]byte(newValue.String), &v); err == nil {
					es.Resources[field.String] = v
				}
			}

		case "entity_spawned":
			if _, exists := ws.Entities[entityID]; !exists {
				ws.Entities[entityID] = &EntityState{
					ID:        entityID,
					Resources: make(map[string]any),
					Params:    make(map[string]any),
				}
			}
			if meta.Valid {
				var m map[string]any
				if json.Unmarshal([]byte(meta.String), &m) == nil {
					if t, ok := m["type"].(string); ok {
						ws.Entities[entityID].TypeName = t
					}
					if loc, ok := m["location"].(string); ok {
						ws.Entities[entityID].Location = loc
					}
				}
			}
			if newValue.Valid {
				var res map[string]any
				if json.Unmarshal([]byte(newValue.String), &res) == nil {
					for k, v := range res {
						ws.Entities[entityID].Resources[k] = v
					}
				}
			}

		case "entity_destroyed":
			delete(ws.Entities, entityID)

		case "entity_moved":
			if newValue.Valid {
				var newLoc string
				if json.Unmarshal([]byte(newValue.String), &newLoc) == nil {
					es := ws.ensureEntity(entityID)
					es.Location = newLoc
				}
			}

		case "connection_added":
			if meta.Valid {
				var m struct {
					From     string  `json:"from"`
					To       string  `json:"to"`
					Type     string  `json:"type"`
					Weight   float64 `json:"weight"`
					Directed bool    `json:"directed"`
				}
				if json.Unmarshal([]byte(meta.String), &m) == nil {
					ws.Connections = append(ws.Connections, ConnectionState{
						From: m.From, To: m.To, Type: m.Type, Weight: m.Weight, Directed: m.Directed,
					})
				}
			}

		case "connection_removed":
			if meta.Valid {
				var m struct {
					From string `json:"from"`
					To   string `json:"to"`
					Type string `json:"type"`
				}
				if json.Unmarshal([]byte(meta.String), &m) == nil {
					ws.removeConnection(m.From, m.To, m.Type)
				}
			}
		}
	}
	return rows.Err()
}

func (ws *WorldState) ensureEntity(id string) *EntityState {
	if es, ok := ws.Entities[id]; ok {
		return es
	}
	es := &EntityState{ID: id, Resources: make(map[string]any), Params: make(map[string]any)}
	ws.Entities[id] = es
	return es
}

func (ws *WorldState) removeConnection(from, to, connType string) {
	out := ws.Connections[:0]
	for _, c := range ws.Connections {
		if c.From == from && c.To == to && c.Type == connType {
			continue
		}
		out = append(out, c)
	}
	ws.Connections = out
}
