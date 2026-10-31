-- The initial schema. See docs/server-v1.md §3.2.

CREATE TABLE flows (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  document   TEXT NOT NULL,
  revision   INTEGER NOT NULL,
  enabled    INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE executions (
  id             TEXT PRIMARY KEY,
  flow_id        TEXT NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
  revision       INTEGER NOT NULL,
  trigger_id     TEXT NOT NULL,
  status         TEXT NOT NULL,
  error          TEXT,
  started_at     TEXT NOT NULL,
  finished_at    TEXT,
  trace_complete INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX executions_by_flow ON executions(flow_id, started_at DESC, id DESC);

CREATE TABLE trace_events (
  execution_id TEXT NOT NULL REFERENCES executions(id) ON DELETE CASCADE,
  seq          INTEGER NOT NULL,
  event        TEXT NOT NULL,
  PRIMARY KEY (execution_id, seq)
);
