package worldengine

import "database/sql"

const createSchemaSQL = `
CREATE TABLE IF NOT EXISTS run_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS types (
    name            TEXT PRIMARY KEY,
    params_schema   TEXT NOT NULL,
    resource_schema TEXT NOT NULL,
    visibility      TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS initial_entities (
    entity_id TEXT PRIMARY KEY,
    type_name TEXT NOT NULL,
    params    TEXT NOT NULL,
    resources TEXT NOT NULL,
    location  TEXT
);
CREATE TABLE IF NOT EXISTS initial_connections (
    from_id  TEXT NOT NULL,
    to_id    TEXT NOT NULL,
    type     TEXT NOT NULL,
    weight   REAL NOT NULL,
    directed INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (from_id, to_id, type)
);
CREATE TABLE IF NOT EXISTS events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    tick       INTEGER NOT NULL,
    phase      TEXT NOT NULL,
    event_type TEXT NOT NULL,
    entity_id  TEXT NOT NULL,
    field      TEXT,
    old_value  TEXT,
    new_value  TEXT,
    meta       TEXT
);
CREATE INDEX IF NOT EXISTS idx_events_tick   ON events(tick);
CREATE INDEX IF NOT EXISTS idx_events_entity ON events(entity_id);
CREATE INDEX IF NOT EXISTS idx_events_type   ON events(event_type);
CREATE TABLE IF NOT EXISTS scores (
    tick     INTEGER NOT NULL,
    agent_id TEXT NOT NULL,
    score    REAL NOT NULL,
    PRIMARY KEY (tick, agent_id)
);
CREATE TABLE IF NOT EXISTS snapshots (
    tick        INTEGER PRIMARY KEY,
    entities    TEXT NOT NULL,
    connections TEXT NOT NULL
);
`

func createSchema(db *sql.DB) error {
	_, err := db.Exec(createSchemaSQL)
	return err
}
