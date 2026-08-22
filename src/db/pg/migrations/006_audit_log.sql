-- FR-ENT-1 (backlog H2): append-only audit ledger of every MCP tool call and
-- mutating REST call. Tamper-evident via SHA-256 hash chain stored per row
-- (prev_hash -> entry_hash); raw arguments are never persisted, only their
-- SHA-256 digest (NFR-2).
--
-- Append-only is enforced AT THE DATABASE: BEFORE UPDATE OR DELETE triggers
-- raise an exception, so even a direct psql session cannot rewrite history.

CREATE TABLE IF NOT EXISTS audit_log(
  id BIGSERIAL PRIMARY KEY,
  ts TIMESTAMPTZ NOT NULL DEFAULT now(),
  actor TEXT NOT NULL DEFAULT 'local',
  agent_client TEXT,
  tool TEXT NOT NULL,
  project TEXT,
  args_hash TEXT NOT NULL,
  result_status TEXT NOT NULL,
  prev_hash TEXT,
  entry_hash TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS audit_log_ts_idx ON audit_log(ts);
CREATE INDEX IF NOT EXISTS audit_log_tool_idx ON audit_log(tool);
-- append-only enforcement:
CREATE OR REPLACE FUNCTION audit_log_no_mutation() RETURNS trigger AS $$
BEGIN RAISE EXCEPTION 'audit_log is append-only'; END $$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS audit_log_ro ON audit_log;
CREATE TRIGGER audit_log_ro BEFORE UPDATE OR DELETE ON audit_log
FOR EACH ROW EXECUTE FUNCTION audit_log_no_mutation();
