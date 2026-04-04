package worldengine

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// RunLog is the per-run SQLite event log.
// It is created when logging is enabled in Config.Log and closed at the end of Run.
type RunLog struct {
	db   *sql.DB
	path string
}

// openRunLog creates (or opens) a new run log file at
// {dir}/{worldName}_{timestamp}_{runID}.db and initialises the schema.
func openRunLog(cfg LogConfig, worldName string) (*RunLog, error) {
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("runlog: mkdir: %w", err)
	}
	if worldName == "" {
		worldName = "world"
	}
	ts := time.Now().UTC().Format("20060102T150405")
	runID := fmt.Sprintf("%06x", rand.Int63()&0xFFFFFF)
	filename := fmt.Sprintf("%s_%s_%s.db", worldName, ts, runID)
	path := filepath.Join(cfg.Dir, filename)

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("runlog: open db: %w", err)
	}
	// Enable WAL for better concurrent read/write.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("runlog: wal: %w", err)
	}
	if err := createSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("runlog: schema: %w", err)
	}
	return &RunLog{db: db, path: path}, nil
}

// Path returns the SQLite file path.
func (rl *RunLog) Path() string { return rl.path }

// DB returns the underlying *sql.DB for direct queries (e.g., by the MCP server).
func (rl *RunLog) DB() *sql.DB { return rl.db }

// Close finalises the run log.
func (rl *RunLog) Close() error { return rl.db.Close() }

// ---------------------------------------------------------------------------
// Run start: metadata, types, initial state
// ---------------------------------------------------------------------------

// WriteMeta inserts run_meta rows at run start.
func (rl *RunLog) WriteMeta(cfg Config) error {
	si := cfg.Log.SnapshotInterval
	if si == 0 {
		si = 100
	}
	rows := [][2]string{
		{"world_name", cfg.Name},
		{"dt", fmt.Sprintf("%g", cfg.DT)},
		{"tick_unit", cfg.TickUnit},
		{"max_ticks", fmt.Sprintf("%d", cfg.MaxTicks)},
		{"max_actions_per_tick", fmt.Sprintf("%d", cfg.MaxActionsPerTick)},
		{"snapshot_interval", fmt.Sprintf("%d", si)},
		{"started_at", time.Now().UTC().Format(time.RFC3339)},
		{"engine_version", "v4"},
	}
	tx, err := rl.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, kv := range rows {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO run_meta(key,value) VALUES(?,?)`, kv[0], kv[1]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// WriteMetaEnd updates run_meta at run end.
func (rl *RunLog) WriteMetaEnd(finalTick int, status string) error {
	rows := [][2]string{
		{"completed_at", time.Now().UTC().Format(time.RFC3339)},
		{"final_tick", fmt.Sprintf("%d", finalTick)},
		{"status", status},
	}
	tx, err := rl.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, kv := range rows {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO run_meta(key,value) VALUES(?,?)`, kv[0], kv[1]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// WriteTypes records type definitions to the types table.
func (rl *RunLog) WriteTypes(w *World) error {
	names := w.registry.Names()
	if len(names) == 0 {
		return nil
	}
	tx, err := rl.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, name := range names {
		td, _ := w.registry.Get(name)
		visMap := make(map[string]string)
		for k := range td.hidden {
			visMap[k] = "hidden"
		}
		for k := range td.private {
			visMap[k] = "private"
		}
		_, err := tx.Exec(
			`INSERT OR REPLACE INTO types(name,params_schema,resource_schema,visibility) VALUES(?,?,?,?)`,
			name,
			jsonEncode(map[string]any(td.params)),
			jsonEncode(map[string]any(td.resources)),
			jsonEncode(visMap),
		)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// WriteInitialState writes initial_entities and initial_connections (tick 0).
func (rl *RunLog) WriteInitialState(w *World) error {
	tx, err := rl.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	ids := w.sortedEntityIDs()
	for _, id := range ids {
		e := w.entities[id]
		location := w.graph.LocationOf(id)
		var locVal any = nil
		if location != "" {
			locVal = location
		}
		_, err := tx.Exec(
			`INSERT OR IGNORE INTO initial_entities(entity_id,type_name,params,resources,location) VALUES(?,?,?,?,?)`,
			id, e.typeName,
			jsonEncode(w.resources.AllParams(id)),
			jsonEncode(w.resources.AllResources(id)),
			locVal,
		)
		if err != nil {
			return err
		}
	}

	for _, c := range w.graph.AllEdges() {
		directed := 0
		if c.Directed {
			directed = 1
		}
		_, err := tx.Exec(
			`INSERT OR IGNORE INTO initial_connections(from_id,to_id,type,weight,directed) VALUES(?,?,?,?,?)`,
			c.From, c.To, c.Type, c.Weight, directed,
		)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---------------------------------------------------------------------------
// Per-tick flush
// ---------------------------------------------------------------------------

// logEvent is one row destined for the events table.
type logEvent struct {
	tick      int
	phase     string
	eventType string
	entityID  string
	field     string
	oldValue  string
	newValue  string
	meta      string
}

// FlushTick writes all events for one tick in a single transaction.
func (rl *RunLog) FlushTick(
	tick int,
	resourceDeltas []Delta,
	dispatched []dispatchedAction,
	moves []movedEntity,
	spawns []pendingSpawn,
	destroys []pendingDestroy,
	connects []pendingConnect,
	disconnects []pendingDisconnect,
) error {
	var evts []logEvent

	// resource_set events from ResourceTable deltas
	for _, d := range resourceDeltas {
		evts = append(evts, logEvent{
			tick:      tick,
			phase:     "tick",
			eventType: "resource_set",
			entityID:  d.EntityID,
			field:     d.Field,
			oldValue:  jsonEncode(d.OldValue),
			newValue:  jsonEncode(d.NewValue),
			meta:      `{"source":"tick"}`,
		})
	}

	// action_invoked events
	for _, da := range dispatched {
		result := "ok"
		reason := ""
		if !da.result.ok {
			result = "failed"
			reason = da.result.reason
		}
		targetID := ""
		if da.target != nil {
			targetID = da.target.id
		}
		meta := map[string]any{
			"action":     da.pending.name,
			"invoker_id": da.pending.invokerID,
			"target_id":  targetID,
			"params":     map[string]any(da.pending.params),
			"result":     result,
			"reason":     reason,
		}
		evts = append(evts, logEvent{
			tick:      tick,
			phase:     "action",
			eventType: "action_invoked",
			entityID:  da.pending.invokerID,
			meta:      jsonEncode(meta),
		})
	}

	// entity_moved events
	for _, m := range moves {
		meta := map[string]any{
			"connection_type":   m.conn.Type,
			"connection_weight": m.conn.Weight,
		}
		evts = append(evts, logEvent{
			tick:      tick,
			phase:     "transition",
			eventType: "entity_moved",
			entityID:  m.entityID,
			field:     "location",
			oldValue:  jsonEncode(m.fromLocID),
			newValue:  jsonEncode(m.toLocID),
			meta:      jsonEncode(meta),
		})
	}

	// entity_spawned events
	for _, s := range spawns {
		meta := map[string]any{
			"type":       s.typeName,
			"spawned_by": s.spawnedBy,
		}
		if s.init.Location != "" {
			meta["location"] = s.init.Location
		}
		evts = append(evts, logEvent{
			tick:      tick,
			phase:     "tick",
			eventType: "entity_spawned",
			entityID:  s.id,
			newValue:  jsonEncode(map[string]any(s.init.Resources)),
			meta:      jsonEncode(meta),
		})
	}

	// entity_destroyed events
	for _, d := range destroys {
		evts = append(evts, logEvent{
			tick:      tick,
			phase:     "tick",
			eventType: "entity_destroyed",
			entityID:  d.id,
			meta:      jsonEncode(map[string]any{"destroyed_by": d.destroyedBy}),
		})
	}

	// connection_added events
	for _, c := range connects {
		meta := map[string]any{
			"from":     c.fromID,
			"to":       c.toID,
			"type":     c.connType,
			"weight":   c.weight,
			"directed": c.directed,
		}
		evts = append(evts, logEvent{
			tick:      tick,
			phase:     "tick",
			eventType: "connection_added",
			entityID:  c.fromID,
			meta:      jsonEncode(meta),
		})
	}

	// connection_removed events
	for _, d := range disconnects {
		meta := map[string]any{
			"from": d.fromID,
			"to":   d.toID,
			"type": d.connType,
		}
		evts = append(evts, logEvent{
			tick:      tick,
			phase:     "tick",
			eventType: "connection_removed",
			entityID:  d.fromID,
			meta:      jsonEncode(meta),
		})
	}

	if len(evts) == 0 {
		return nil
	}

	tx, err := rl.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO events(tick,phase,event_type,entity_id,field,old_value,new_value,meta) VALUES(?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, ev := range evts {
		fieldVal := sqlNullableString(ev.field)
		oldVal := sqlNullableString(ev.oldValue)
		newVal := sqlNullableString(ev.newValue)
		metaVal := sqlNullableString(ev.meta)
		if _, err := stmt.Exec(ev.tick, ev.phase, ev.eventType, ev.entityID, fieldVal, oldVal, newVal, metaVal); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// WriteScore records a score observation for a given agent+tick.
func (rl *RunLog) WriteScore(tick int, agentID string, score float64) error {
	_, err := rl.db.Exec(
		`INSERT OR REPLACE INTO scores(tick,agent_id,score) VALUES(?,?,?)`,
		tick, agentID, score,
	)
	return err
}

// ---------------------------------------------------------------------------
// Snapshots
// ---------------------------------------------------------------------------

// WriteSnapshot writes a full entity+connection state snapshot at the given tick.
func (rl *RunLog) WriteSnapshot(tick int, w *World) error {
	entitiesJSON := buildEntitiesJSON(w)
	connectionsJSON := buildConnectionsJSON(w)

	_, err := rl.db.Exec(
		`INSERT OR REPLACE INTO snapshots(tick,entities,connections) VALUES(?,?,?)`,
		tick, entitiesJSON, connectionsJSON,
	)
	return err
}

// buildEntitiesJSON encodes all entities as a JSON object keyed by entity ID.
func buildEntitiesJSON(w *World) string {
	m := make(map[string]any, len(w.entities))
	for id, e := range w.entities {
		m[id] = map[string]any{
			"type":      e.typeName,
			"params":    w.resources.AllParams(id),
			"resources": w.resources.AllResources(id),
			"location":  w.graph.LocationOf(id),
		}
	}
	return jsonEncode(m)
}

// buildConnectionsJSON encodes the connection graph as a JSON array.
func buildConnectionsJSON(w *World) string {
	edges := w.graph.AllEdges()
	out := make([]map[string]any, 0, len(edges))
	for _, c := range edges {
		out = append(out, map[string]any{
			"from":     c.From,
			"to":       c.To,
			"type":     c.Type,
			"weight":   c.Weight,
			"directed": c.Directed,
		})
	}
	return jsonEncode(out)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// jsonEncode returns a JSON string for any value, handling engine-specific types.
func jsonEncode(v any) string {
	switch t := v.(type) {
	case Set:
		b, _ := json.Marshal(t.Slice())
		return string(b)
	case Queue:
		b, _ := json.Marshal(t.Slice())
		return string(b)
	case nil:
		return "null"
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// sqlNullableString returns nil for empty strings (stored as NULL) or the string.
func sqlNullableString(s string) any {
	if s == "" || s == "null" {
		return nil
	}
	return s
}

// sortedEntityIDs returns all entity IDs in sorted order.
func (w *World) sortedEntityIDs() []string {
	ids := make([]string, 0, len(w.entities))
	for id := range w.entities {
		ids = append(ids, id)
	}
	sortStrings(ids)
	return ids
}
