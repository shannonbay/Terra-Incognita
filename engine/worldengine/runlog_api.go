package worldengine

import (
	"database/sql"
	"fmt"
)

// ---------------------------------------------------------------------------
// Public API — spec §11.10
// ---------------------------------------------------------------------------

// OpenRunLog opens an existing run log file for reading.
func OpenRunLog(path string) (*RunLog, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("runlog: open: %w", err)
	}
	return &RunLog{db: db, path: path}, nil
}

// ---------------------------------------------------------------------------
// RunMeta
// ---------------------------------------------------------------------------

// RunMeta holds the run_meta key/value pairs.
type RunMeta struct {
	WorldName          string
	DT                 string
	TickUnit           string
	MaxTicks           string
	MaxActionsPerTick  string
	SnapshotInterval   string
	StartedAt          string
	CompletedAt        string
	FinalTick          string
	Status             string
	EngineVersion      string
}

// Meta returns the run metadata from run_meta.
func (rl *RunLog) Meta() (RunMeta, error) {
	rows, err := rl.db.Query(`SELECT key, value FROM run_meta`)
	if err != nil {
		return RunMeta{}, err
	}
	defer rows.Close()

	kv := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return RunMeta{}, err
		}
		kv[k] = v
	}
	return RunMeta{
		WorldName:         kv["world_name"],
		DT:                kv["dt"],
		TickUnit:          kv["tick_unit"],
		MaxTicks:          kv["max_ticks"],
		MaxActionsPerTick: kv["max_actions_per_tick"],
		SnapshotInterval:  kv["snapshot_interval"],
		StartedAt:         kv["started_at"],
		CompletedAt:       kv["completed_at"],
		FinalTick:         kv["final_tick"],
		Status:            kv["status"],
		EngineVersion:     kv["engine_version"],
	}, rows.Err()
}

// ---------------------------------------------------------------------------
// State reconstruction
// ---------------------------------------------------------------------------

// StateAt reconstructs and returns the full world state at the given tick.
func (rl *RunLog) StateAt(tick int) (*WorldState, error) {
	r := &StateReconstructor{}
	return r.StateAt(rl.db, tick)
}

// ---------------------------------------------------------------------------
// Event queries
// ---------------------------------------------------------------------------

// EventQuery filters the events table.
type EventQuery struct {
	TickFrom  int    // inclusive, 0 = no lower bound
	TickTo    int    // inclusive, 0 = no upper bound
	EntityID  string // "" = all entities
	EventType string // "" = all event types
}

// LoggedEvent is one row from the events table.
type LoggedEvent struct {
	ID        int64
	Tick      int
	Phase     string
	EventType string
	EntityID  string
	Field     string
	OldValue  string
	NewValue  string
	Meta      string
}

// Events queries the events table with optional filters.
func (rl *RunLog) Events(q EventQuery) ([]LoggedEvent, error) {
	query := `SELECT id, tick, phase, event_type, entity_id,
	                 COALESCE(field,''), COALESCE(old_value,''), COALESCE(new_value,''), COALESCE(meta,'')
	          FROM events WHERE 1=1`
	var args []any
	if q.TickFrom > 0 {
		query += " AND tick >= ?"
		args = append(args, q.TickFrom)
	}
	if q.TickTo > 0 {
		query += " AND tick <= ?"
		args = append(args, q.TickTo)
	}
	if q.EntityID != "" {
		query += " AND entity_id = ?"
		args = append(args, q.EntityID)
	}
	if q.EventType != "" {
		query += " AND event_type = ?"
		args = append(args, q.EventType)
	}
	query += " ORDER BY id"

	rows, err := rl.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []LoggedEvent
	for rows.Next() {
		var ev LoggedEvent
		if err := rows.Scan(&ev.ID, &ev.Tick, &ev.Phase, &ev.EventType, &ev.EntityID,
			&ev.Field, &ev.OldValue, &ev.NewValue, &ev.Meta); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Score queries
// ---------------------------------------------------------------------------

// TickScore is one score observation.
type TickScore struct {
	Tick  int
	Score float64
}

// Scores returns the score history for the given agent.
func (rl *RunLog) Scores(agentID string) ([]TickScore, error) {
	rows, err := rl.db.Query(
		`SELECT tick, score FROM scores WHERE agent_id = ? ORDER BY tick`,
		agentID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TickScore
	for rows.Next() {
		var ts TickScore
		if err := rows.Scan(&ts.Tick, &ts.Score); err != nil {
			return nil, err
		}
		out = append(out, ts)
	}
	return out, rows.Err()
}
