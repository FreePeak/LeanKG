package telemetry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// SQLiteStore is the DS-03 ledger. Several processes (one stdio MCP server
// per agent plus HTTP servers) write the same file, so writes run inside
// BEGIN IMMEDIATE: a deferred transaction that reads before it writes can
// fail at once with SQLITE_BUSY_SNAPSHOT instead of waiting out busy_timeout.
type SQLiteStore struct {
	db       *sql.DB
	path     string
	readOnly bool
}

// querier is the subset of *sql.DB and *sql.Conn the helpers need.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// sessionWindow is the heuristic grouping gap (DS-03, plan 1.4).
const sessionWindow = 30 * time.Minute

// schemaMigrations run in order; each one is recorded in schema_migrations.
var schemaMigrations = []string{`
CREATE TABLE calls (
  id TEXT PRIMARY KEY,
  ts INTEGER NOT NULL,
  latency_ms INTEGER NOT NULL DEFAULT 0,
  transport TEXT NOT NULL DEFAULT '',
  method TEXT NOT NULL DEFAULT '',
  tool TEXT NOT NULL DEFAULT '',
  action TEXT NOT NULL DEFAULT '',
  command TEXT NOT NULL DEFAULT '',
  project TEXT NOT NULL DEFAULT '',
  client_name TEXT NOT NULL DEFAULT '',
  client_version TEXT NOT NULL DEFAULT '',
  client_session_id TEXT NOT NULL DEFAULT '',
  cwd TEXT NOT NULL DEFAULT '',
  session_id TEXT NOT NULL DEFAULT '',
  correlation TEXT NOT NULL DEFAULT '',
  args_hash TEXT NOT NULL DEFAULT '',
  arg_keys TEXT NOT NULL DEFAULT '',
  args_redacted TEXT NOT NULL DEFAULT '',
  outcome TEXT NOT NULL DEFAULT '',
  outcome_reason TEXT NOT NULL DEFAULT '',
  error_code TEXT NOT NULL DEFAULT '',
  rung TEXT NOT NULL DEFAULT '',
  confidence TEXT NOT NULL DEFAULT '',
  freshness TEXT NOT NULL DEFAULT '',
  hits INTEGER NOT NULL DEFAULT 0,
  hit_files TEXT NOT NULL DEFAULT '',
  out_tokens_pre INTEGER NOT NULL DEFAULT 0,
  out_tokens_post INTEGER NOT NULL DEFAULT 0,
  budget_trimmed_tokens INTEGER NOT NULL DEFAULT 0,
  baseline_tokens INTEGER NOT NULL DEFAULT 0,
  baseline_method TEXT NOT NULL DEFAULT '',
  tokens_saved INTEGER NOT NULL DEFAULT 0,
  body_redacted TEXT NOT NULL DEFAULT ''
);
CREATE INDEX calls_ts ON calls(ts);
CREATE INDEX calls_session ON calls(session_id);
CREATE INDEX calls_client_session ON calls(client_session_id);
CREATE INDEX calls_outcome ON calls(outcome);
CREATE TABLE memory_events (
  id TEXT PRIMARY KEY,
  ts INTEGER NOT NULL,
  latency_ms INTEGER NOT NULL DEFAULT 0,
  verb TEXT NOT NULL DEFAULT '',
  transport TEXT NOT NULL DEFAULT '',
  session_id TEXT NOT NULL DEFAULT '',
  client_name TEXT NOT NULL DEFAULT '',
  client_version TEXT NOT NULL DEFAULT '',
  client_session_id TEXT NOT NULL DEFAULT '',
  cwd TEXT NOT NULL DEFAULT '',
  banks TEXT NOT NULL DEFAULT '',
  query_hash TEXT NOT NULL DEFAULT '',
  limit_n INTEGER NOT NULL DEFAULT 0,
  returned INTEGER NOT NULL DEFAULT 0,
  returned_ids TEXT NOT NULL DEFAULT '',
  scores TEXT NOT NULL DEFAULT '',
  dense_hits INTEGER NOT NULL DEFAULT 0,
  age_median_s INTEGER NOT NULL DEFAULT 0,
  age_max_s INTEGER NOT NULL DEFAULT 0,
  written INTEGER NOT NULL DEFAULT 0,
  skipped INTEGER NOT NULL DEFAULT 0,
  replaced INTEGER NOT NULL DEFAULT 0,
  deleted INTEGER NOT NULL DEFAULT 0,
  deduped INTEGER NOT NULL DEFAULT 0,
  tokens INTEGER NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX memory_ts ON memory_events(ts);
CREATE INDEX memory_session ON memory_events(session_id);
CREATE TABLE sessions (
  id TEXT PRIMARY KEY,
  client_name TEXT NOT NULL DEFAULT '',
  client_session_id TEXT NOT NULL DEFAULT '',
  project TEXT NOT NULL DEFAULT '',
  cwd TEXT NOT NULL DEFAULT '',
  first_ts INTEGER NOT NULL,
  last_ts INTEGER NOT NULL,
  correlation TEXT NOT NULL DEFAULT '',
  transcript_path TEXT NOT NULL DEFAULT '',
  link_status TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sessions_client_last ON sessions(client_name, last_ts);
CREATE TABLE session_links (
  call_id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  confidence REAL NOT NULL DEFAULT 0,
  tool_use_id TEXT NOT NULL DEFAULT '',
  window TEXT NOT NULL DEFAULT '',
  linked_at INTEGER NOT NULL
);
CREATE INDEX links_session ON session_links(session_id);
CREATE TABLE ab_runs (
  id TEXT PRIMARY KEY,
  source TEXT NOT NULL DEFAULT '',
  task TEXT NOT NULL DEFAULT '',
  arm TEXT NOT NULL DEFAULT '',
  repo TEXT NOT NULL DEFAULT '',
  tokens INTEGER NOT NULL DEFAULT 0,
  turns INTEGER NOT NULL DEFAULT 0,
  duration_s REAL NOT NULL DEFAULT 0,
  tool_calls INTEGER NOT NULL DEFAULT 0,
  file_reads INTEGER NOT NULL DEFAULT 0,
  cost_usd REAL NOT NULL DEFAULT 0,
  judge_score REAL NOT NULL DEFAULT 0,
  valid INTEGER NOT NULL DEFAULT 0,
  imported_at INTEGER NOT NULL
);
CREATE TABLE meta (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
INSERT INTO meta(key, value) VALUES ('dropped_events', '0');
`}

// OpenSQLite opens the ledger at path. readOnly never creates the file and
// returns an error wrapping os.ErrNotExist when it is missing (DS-03).
func OpenSQLite(path string, readOnly bool) (*SQLiteStore, error) {
	if readOnly {
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("telemetry: open %s: %w", path, err)
		}
	} else if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("telemetry: create dir: %w", err)
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(10000)"
	if readOnly {
		dsn += "&_pragma=query_only(ON)"
	} else {
		dsn += "&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("telemetry: open %s: %w", path, err)
	}
	// One connection per handle keeps this process's writes in order.
	db.SetMaxOpenConns(1)
	s := &SQLiteStore{db: db, path: path, readOnly: readOnly}
	if !readOnly {
		if err := s.migrate(context.Background()); err != nil {
			db.Close()
			return nil, err
		}
	}
	return s, nil
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("telemetry: migrations table: %w", err)
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("telemetry: migrate: %w", err)
	}
	defer conn.Close()
	return withWriteTx(ctx, conn, func(q querier) error {
		var cur int
		if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&cur); err != nil {
			return err
		}
		for i := cur; i < len(schemaMigrations); i++ {
			if _, err := q.ExecContext(ctx, schemaMigrations[i]); err != nil {
				return fmt.Errorf("telemetry: migration %d: %w", i+1, err)
			}
			if _, err := q.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`, i+1, nowNanos()); err != nil {
				return err
			}
		}
		return nil
	})
}

// withWriteTx runs fn inside BEGIN IMMEDIATE on conn and commits or rolls back.
func withWriteTx(ctx context.Context, conn *sql.Conn, fn func(q querier) error) error {
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("telemetry: begin: %w", err)
	}
	if err := fn(conn); err != nil {
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		return fmt.Errorf("telemetry: commit: %w", err)
	}
	return nil
}

// write runs fn in a write transaction on a dedicated connection.
func (s *SQLiteStore) write(ctx context.Context, fn func(q querier) error) error {
	if s.readOnly {
		return errors.New("telemetry: store is read-only")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("telemetry: conn: %w", err)
	}
	defer conn.Close()
	return withWriteTx(ctx, conn, fn)
}

// Close closes the underlying database.
func (s *SQLiteStore) Close() error { return s.db.Close() }

// Path returns the ledger file path.
func (s *SQLiteStore) Path() string { return s.path }

// nowNanos is the wall clock in the unit the ledger stores timestamps in.
func nowNanos() int64 { return time.Now().UnixNano() }

// toNanos stores a time as unix nanoseconds; the zero time becomes 0.
func toNanos(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

// fromNanos is the inverse of toNanos.
func fromNanos(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}

// sessionIDFor returns the exact session id for a client session, or "".
func sessionIDFor(client, clientSessionID string) string {
	if clientSessionID == "" {
		return ""
	}
	return client + ":" + clientSessionID
}

// assignSession resolves the session an event belongs to (DS-03): exact by
// client session id; else extend the latest heuristic session of the same
// client and cwd (or project) whose last event is within 30 minutes of ts;
// else start one named after firstID.
func assignSession(ctx context.Context, q querier, client, clientSessionID, cwd, project string, ts time.Time, firstID string) (string, string, error) {
	if exact := sessionIDFor(client, clientSessionID); exact != "" {
		return exact, CorrExact, nil
	}
	lo := toNanos(ts.Add(-sessionWindow))
	hi := toNanos(ts.Add(sessionWindow))
	var (
		where string
		key   string
	)
	if cwd != "" {
		where, key = "cwd = ?", cwd
	} else {
		where, key = "project = ? AND cwd = ''", project
	}
	var id string
	err := q.QueryRowContext(ctx, `SELECT id FROM sessions
		WHERE client_name = ? AND correlation = ? AND `+where+` AND last_ts BETWEEN ? AND ?
		ORDER BY last_ts DESC LIMIT 1`,
		client, CorrHeuristic, key, lo, hi).Scan(&id)
	if err == nil {
		return id, CorrHeuristic, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", "", fmt.Errorf("telemetry: find session: %w", err)
	}
	return client + ":h:" + firstID, CorrHeuristic, nil
}

// upsertSession inserts s or widens the stored row: first_ts moves earlier,
// last_ts later, and empty identity fields are filled once.
func upsertSession(ctx context.Context, q querier, s Session) error {
	_, err := q.ExecContext(ctx, `INSERT INTO sessions
		(id, client_name, client_session_id, project, cwd, first_ts, last_ts, correlation, transcript_path, link_status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		  first_ts = MIN(sessions.first_ts, excluded.first_ts),
		  last_ts = MAX(sessions.last_ts, excluded.last_ts),
		  project = CASE WHEN sessions.project = '' THEN excluded.project ELSE sessions.project END,
		  cwd = CASE WHEN sessions.cwd = '' THEN excluded.cwd ELSE sessions.cwd END,
		  client_session_id = CASE WHEN sessions.client_session_id = '' THEN excluded.client_session_id ELSE sessions.client_session_id END,
		  transcript_path = CASE WHEN excluded.transcript_path = '' THEN sessions.transcript_path ELSE excluded.transcript_path END,
		  link_status = CASE WHEN excluded.link_status = '' THEN sessions.link_status ELSE excluded.link_status END`,
		s.ID, s.ClientName, s.ClientSessionID, s.Project, s.Cwd,
		toNanos(s.FirstTS), toNanos(s.LastTS), s.Correlation, s.TranscriptPath, s.LinkStatus)
	if err != nil {
		return fmt.Errorf("telemetry: upsert session: %w", err)
	}
	return nil
}

// InsertCalls stores calls in one transaction. It assigns SessionID and
// Correlation when empty and keeps the sessions table current.
func (s *SQLiteStore) InsertCalls(ctx context.Context, calls []CallEvent) error {
	if len(calls) == 0 {
		return nil
	}
	return s.write(ctx, func(q querier) error {
		for i := range calls {
			c := &calls[i]
			if c.ID == "" {
				c.ID = newEventID(time.Now())
			}
			if c.TS.IsZero() {
				c.TS = time.Now()
			}
			if c.ClientName == "" {
				c.ClientName = "unknown"
			}
			if c.SessionID == "" {
				sid, corr, err := assignSession(ctx, q, c.ClientName, c.ClientSessionID, c.Cwd, c.Project, c.TS, c.ID)
				if err != nil {
					return err
				}
				c.SessionID, c.Correlation = sid, corr
			}
			if c.Correlation == "" {
				c.Correlation = c.Identity.Correlation()
			}
			if err := upsertSession(ctx, q, Session{
				ID: c.SessionID, ClientName: c.ClientName, ClientSessionID: c.ClientSessionID,
				Project: c.Project, Cwd: c.Cwd, FirstTS: c.TS, LastTS: c.TS, Correlation: c.Correlation,
			}); err != nil {
				return err
			}
			if _, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO calls (
				id, ts, latency_ms, transport, method, tool, action, command, project,
				client_name, client_version, client_session_id, cwd, session_id, correlation,
				args_hash, arg_keys, args_redacted, outcome, outcome_reason, error_code,
				rung, confidence, freshness, hits, hit_files,
				out_tokens_pre, out_tokens_post, budget_trimmed_tokens, baseline_tokens, baseline_method,
				tokens_saved, body_redacted)
				VALUES (?,?,?,?,?,?,?,?,?, ?,?,?,?,?,?, ?,?,?,?,?,?, ?,?,?,?,?, ?,?,?,?,?, ?,?)`,
				c.ID, toNanos(c.TS), c.LatencyMS, c.Transport, c.Method, c.Tool, c.Action, c.Command, c.Project,
				c.ClientName, c.ClientVersion, c.ClientSessionID, c.Cwd, c.SessionID, c.Correlation,
				c.ArgsHash, c.ArgKeys, c.ArgsRedacted, c.Outcome, c.OutcomeReason, c.ErrorCode,
				c.Rung, c.Confidence, c.Freshness, c.Hits, c.HitFiles,
				c.OutTokensPre, c.OutTokensPost, c.BudgetTrimmedTokens, c.BaselineTokens, c.BaselineMethod,
				c.TokensSaved, c.BodyRedacted); err != nil {
				return fmt.Errorf("telemetry: insert call: %w", err)
			}
		}
		return nil
	})
}

// InsertMemoryEvents stores memory events with the same session assignment.
func (s *SQLiteStore) InsertMemoryEvents(ctx context.Context, evs []MemoryEvent) error {
	if len(evs) == 0 {
		return nil
	}
	return s.write(ctx, func(q querier) error {
		for i := range evs {
			e := &evs[i]
			if e.ID == "" {
				e.ID = newEventID(time.Now())
			}
			if e.TS.IsZero() {
				e.TS = time.Now()
			}
			if e.ClientName == "" {
				e.ClientName = "unknown"
			}
			if e.SessionID == "" {
				sid, _, err := assignSession(ctx, q, e.ClientName, e.ClientSessionID, e.Cwd, "", e.TS, e.ID)
				if err != nil {
					return err
				}
				e.SessionID = sid
			}
			if err := upsertSession(ctx, q, Session{
				ID: e.SessionID, ClientName: e.ClientName, ClientSessionID: e.ClientSessionID,
				Cwd: e.Cwd, FirstTS: e.TS, LastTS: e.TS, Correlation: e.Identity.Correlation(),
			}); err != nil {
				return err
			}
			if _, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO memory_events (
				id, ts, latency_ms, verb, transport, session_id, client_name, client_version, client_session_id, cwd,
				banks, query_hash, limit_n, returned, returned_ids, scores, dense_hits, age_median_s, age_max_s,
				written, skipped, replaced, deleted, deduped, tokens, error)
				VALUES (?,?,?,?,?,?,?,?,?,?, ?,?,?,?,?,?,?,?,?, ?,?,?,?,?,?,?)`,
				e.ID, toNanos(e.TS), e.LatencyMS, e.Verb, e.Transport, e.SessionID, e.ClientName, e.ClientVersion,
				e.ClientSessionID, e.Cwd, e.Banks, e.QueryHash, e.Limit, e.Returned, e.ReturnedIDs, e.Scores,
				e.DenseHits, e.AgeMedianS, e.AgeMaxS, e.Written, e.Skipped, e.Replaced, e.Deleted, boolInt(e.Deduped),
				e.Tokens, e.Error); err != nil {
				return fmt.Errorf("telemetry: insert memory event: %w", err)
			}
		}
		return nil
	})
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// UpsertSessions writes session rows, widening existing time ranges.
func (s *SQLiteStore) UpsertSessions(ctx context.Context, ss []Session) error {
	if len(ss) == 0 {
		return nil
	}
	return s.write(ctx, func(q querier) error {
		for _, sess := range ss {
			if err := upsertSession(ctx, q, sess); err != nil {
				return err
			}
		}
		return nil
	})
}

// UpsertLinks stores transcript links; a call is linked at most once.
func (s *SQLiteStore) UpsertLinks(ctx context.Context, ls []SessionLink) error {
	if len(ls) == 0 {
		return nil
	}
	return s.write(ctx, func(q querier) error {
		for _, l := range ls {
			if _, err := q.ExecContext(ctx, `INSERT INTO session_links
				(call_id, session_id, confidence, tool_use_id, window, linked_at)
				VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT(call_id) DO UPDATE SET
				  session_id = excluded.session_id, confidence = excluded.confidence,
				  tool_use_id = excluded.tool_use_id, window = excluded.window, linked_at = excluded.linked_at`,
				l.CallID, l.SessionID, l.Confidence, l.ToolUseID, l.Window, toNanos(l.LinkedAt)); err != nil {
				return fmt.Errorf("telemetry: upsert link: %w", err)
			}
		}
		return nil
	})
}

// InsertABRuns stores imported A/B trials, replacing a run with the same id.
func (s *SQLiteStore) InsertABRuns(ctx context.Context, rs []ABRun) error {
	if len(rs) == 0 {
		return nil
	}
	return s.write(ctx, func(q querier) error {
		for _, r := range rs {
			if _, err := q.ExecContext(ctx, `INSERT OR REPLACE INTO ab_runs
				(id, source, task, arm, repo, tokens, turns, duration_s, tool_calls, file_reads, cost_usd, judge_score, valid, imported_at)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				r.ID, r.Source, r.Task, r.Arm, r.Repo, r.Tokens, r.Turns, r.DurationS, r.ToolCalls,
				r.FileReads, r.CostUSD, r.JudgeScore, boolInt(r.Valid), toNanos(r.ImportedAt)); err != nil {
				return fmt.Errorf("telemetry: insert ab run: %w", err)
			}
		}
		return nil
	})
}

// AddDropped adds n to the dropped_events counter.
func (s *SQLiteStore) AddDropped(ctx context.Context, n int64) error {
	if n == 0 {
		return nil
	}
	return s.write(ctx, func(q querier) error {
		_, err := q.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES ('dropped_events', ?)
			ON CONFLICT(key) DO UPDATE SET value = CAST(CAST(meta.value AS INTEGER) + CAST(excluded.value AS INTEGER) AS TEXT)`,
			fmt.Sprint(n))
		return err
	})
}

// dropped reads the dropped_events counter.
func (s *SQLiteStore) dropped(ctx context.Context) (int64, error) {
	var v int64
	err := s.db.QueryRowContext(ctx, `SELECT CAST(value AS INTEGER) FROM meta WHERE key = 'dropped_events'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return v, err
}

const callColumns = `id, ts, latency_ms, transport, method, tool, action, command, project,
	client_name, client_version, client_session_id, cwd, session_id, correlation,
	args_hash, arg_keys, args_redacted, outcome, outcome_reason, error_code,
	rung, confidence, freshness, hits, hit_files,
	out_tokens_pre, out_tokens_post, budget_trimmed_tokens, baseline_tokens, baseline_method,
	tokens_saved, body_redacted`

// rowScanner is satisfied by *sql.Row and *sql.Rows.
type rowScanner interface{ Scan(dest ...any) error }

func scanCall(r rowScanner) (CallEvent, error) {
	var c CallEvent
	var ts int64
	err := r.Scan(&c.ID, &ts, &c.LatencyMS, &c.Transport, &c.Method, &c.Tool, &c.Action, &c.Command, &c.Project,
		&c.ClientName, &c.ClientVersion, &c.ClientSessionID, &c.Cwd, &c.SessionID, &c.Correlation,
		&c.ArgsHash, &c.ArgKeys, &c.ArgsRedacted, &c.Outcome, &c.OutcomeReason, &c.ErrorCode,
		&c.Rung, &c.Confidence, &c.Freshness, &c.Hits, &c.HitFiles,
		&c.OutTokensPre, &c.OutTokensPost, &c.BudgetTrimmedTokens, &c.BaselineTokens, &c.BaselineMethod,
		&c.TokensSaved, &c.BodyRedacted)
	c.TS = fromNanos(ts)
	return c, err
}

// timeClause appends since/until bounds on a column holding unix nanos.
func timeClause(where []string, args []any, col string, since, until time.Time) ([]string, []any) {
	if !since.IsZero() {
		where = append(where, col+" >= ?")
		args = append(args, toNanos(since))
	}
	if !until.IsZero() {
		where = append(where, col+" < ?")
		args = append(args, toNanos(until))
	}
	return where, args
}

// limitClause appends LIMIT/OFFSET; zero Limit means no limit.
func limitClause(q string, args []any, f CallFilter) (string, []any) {
	if f.Limit > 0 {
		q += " LIMIT ? OFFSET ?"
		args = append(args, f.Limit, f.Offset)
	} else if f.Offset > 0 {
		q += " LIMIT -1 OFFSET ?"
		args = append(args, f.Offset)
	}
	return q, args
}

// Calls lists calls, newest first.
func (s *SQLiteStore) Calls(ctx context.Context, f CallFilter) ([]CallEvent, error) {
	var where []string
	var args []any
	where, args = timeClause(where, args, "ts", f.Since, f.Until)
	if f.Client != "" {
		where = append(where, "client_name = ?")
		args = append(args, f.Client)
	}
	if f.Project != "" {
		where = append(where, "project = ?")
		args = append(args, f.Project)
	}
	if f.SessionID != "" {
		where = append(where, "session_id = ?")
		args = append(args, f.SessionID)
	}
	if f.Outcome == "error" {
		where = append(where, "outcome LIKE 'error:%'")
	} else if f.Outcome != "" {
		where = append(where, "outcome = ?")
		args = append(args, f.Outcome)
	}
	if f.Tool != "" {
		where = append(where, "tool = ?")
		args = append(args, f.Tool)
	}
	q := `SELECT ` + callColumns + ` FROM calls`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY ts DESC, id DESC"
	q, args = limitClause(q, args, f)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("telemetry: calls: %w", err)
	}
	defer rows.Close()
	var out []CallEvent
	for rows.Next() {
		c, err := scanCall(rows)
		if err != nil {
			return nil, fmt.Errorf("telemetry: scan call: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Call returns one call by id.
func (s *SQLiteStore) Call(ctx context.Context, id string) (CallEvent, bool, error) {
	c, err := scanCall(s.db.QueryRowContext(ctx, `SELECT `+callColumns+` FROM calls WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return CallEvent{}, false, nil
	}
	if err != nil {
		return CallEvent{}, false, fmt.Errorf("telemetry: call: %w", err)
	}
	return c, true, nil
}

// MemoryEvents lists memory events, newest first. Tool matches the verb and
// Client matches client_name; the other filter fields do not apply to memory.
func (s *SQLiteStore) MemoryEvents(ctx context.Context, f CallFilter) ([]MemoryEvent, error) {
	var where []string
	var args []any
	where, args = timeClause(where, args, "ts", f.Since, f.Until)
	if f.Client != "" {
		where = append(where, "client_name = ?")
		args = append(args, f.Client)
	}
	if f.SessionID != "" {
		where = append(where, "session_id = ?")
		args = append(args, f.SessionID)
	}
	if f.Tool != "" {
		where = append(where, "verb = ?")
		args = append(args, f.Tool)
	}
	q := `SELECT id, ts, latency_ms, verb, transport, session_id, client_name, client_version, client_session_id, cwd,
		banks, query_hash, limit_n, returned, returned_ids, scores, dense_hits, age_median_s, age_max_s,
		written, skipped, replaced, deleted, deduped, tokens, error FROM memory_events`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY ts DESC, id DESC"
	q, args = limitClause(q, args, f)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("telemetry: memory events: %w", err)
	}
	defer rows.Close()
	var out []MemoryEvent
	for rows.Next() {
		var e MemoryEvent
		var ts int64
		var deduped int
		if err := rows.Scan(&e.ID, &ts, &e.LatencyMS, &e.Verb, &e.Transport, &e.SessionID, &e.ClientName,
			&e.ClientVersion, &e.ClientSessionID, &e.Cwd, &e.Banks, &e.QueryHash, &e.Limit, &e.Returned,
			&e.ReturnedIDs, &e.Scores, &e.DenseHits, &e.AgeMedianS, &e.AgeMaxS, &e.Written, &e.Skipped,
			&e.Replaced, &e.Deleted, &deduped, &e.Tokens, &e.Error); err != nil {
			return nil, fmt.Errorf("telemetry: scan memory event: %w", err)
		}
		e.TS = fromNanos(ts)
		e.Deduped = deduped != 0
		out = append(out, e)
	}
	return out, rows.Err()
}

const sessionColumns = `id, client_name, client_session_id, project, cwd, first_ts, last_ts, correlation, transcript_path, link_status`

func scanSession(r rowScanner) (Session, error) {
	var s Session
	var first, last int64
	err := r.Scan(&s.ID, &s.ClientName, &s.ClientSessionID, &s.Project, &s.Cwd, &first, &last,
		&s.Correlation, &s.TranscriptPath, &s.LinkStatus)
	s.FirstTS, s.LastTS = fromNanos(first), fromNanos(last)
	return s, err
}

// Sessions lists sessions by last activity, newest first. Client, Project,
// SessionID, and the since/until window (on last_ts) apply.
func (s *SQLiteStore) Sessions(ctx context.Context, f CallFilter) ([]Session, error) {
	var where []string
	var args []any
	if !f.Since.IsZero() {
		where = append(where, "last_ts >= ?")
		args = append(args, toNanos(f.Since))
	}
	if !f.Until.IsZero() {
		where = append(where, "first_ts < ?")
		args = append(args, toNanos(f.Until))
	}
	if f.Client != "" {
		where = append(where, "client_name = ?")
		args = append(args, f.Client)
	}
	if f.Project != "" {
		where = append(where, "project = ?")
		args = append(args, f.Project)
	}
	if f.SessionID != "" {
		where = append(where, "id = ?")
		args = append(args, f.SessionID)
	}
	q := `SELECT ` + sessionColumns + ` FROM sessions`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY last_ts DESC, id DESC"
	q, args = limitClause(q, args, f)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("telemetry: sessions: %w", err)
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("telemetry: scan session: %w", err)
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

// Session returns one session by id.
func (s *SQLiteStore) Session(ctx context.Context, id string) (Session, bool, error) {
	sess, err := scanSession(s.db.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM sessions WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, fmt.Errorf("telemetry: session: %w", err)
	}
	return sess, true, nil
}

// Links returns the transcript links of a session, in link order.
func (s *SQLiteStore) Links(ctx context.Context, sessionID string) ([]SessionLink, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT call_id, session_id, confidence, tool_use_id, window, linked_at
		FROM session_links WHERE session_id = ? ORDER BY linked_at, call_id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("telemetry: links: %w", err)
	}
	defer rows.Close()
	var out []SessionLink
	for rows.Next() {
		var l SessionLink
		var at int64
		if err := rows.Scan(&l.CallID, &l.SessionID, &l.Confidence, &l.ToolUseID, &l.Window, &at); err != nil {
			return nil, fmt.Errorf("telemetry: scan link: %w", err)
		}
		l.LinkedAt = fromNanos(at)
		out = append(out, l)
	}
	return out, rows.Err()
}

// UnlinkedCalls returns up to limit calls older than olderThan that have no
// session_links row, oldest first. The linker works through these.
// UnlinkedCalls returns MCP tool calls with no link yet: only they have a
// tool_use entry in an agent transcript (handshake and REST rows never link).
func (s *SQLiteStore) UnlinkedCalls(ctx context.Context, olderThan time.Time, limit int) ([]CallEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+prefixed("c.", callColumns)+` FROM calls c
		LEFT JOIN session_links l ON l.call_id = c.id
		WHERE l.call_id IS NULL AND c.ts < ? AND c.method = ?
		ORDER BY c.ts ASC, c.id ASC LIMIT ?`, toNanos(olderThan), MethodToolsCall, limit)
	if err != nil {
		return nil, fmt.Errorf("telemetry: unlinked calls: %w", err)
	}
	defer rows.Close()
	var out []CallEvent
	for rows.Next() {
		c, err := scanCall(rows)
		if err != nil {
			return nil, fmt.Errorf("telemetry: scan call: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// prefixed qualifies each column in a comma-separated list with prefix.
func prefixed(prefix, cols string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = prefix + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

// ABRuns lists imported A/B trials in import order.
func (s *SQLiteStore) ABRuns(ctx context.Context) ([]ABRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, source, task, arm, repo, tokens, turns, duration_s, tool_calls,
		file_reads, cost_usd, judge_score, valid, imported_at FROM ab_runs ORDER BY imported_at, id`)
	if err != nil {
		return nil, fmt.Errorf("telemetry: ab runs: %w", err)
	}
	defer rows.Close()
	var out []ABRun
	for rows.Next() {
		var r ABRun
		var valid int
		var at int64
		if err := rows.Scan(&r.ID, &r.Source, &r.Task, &r.Arm, &r.Repo, &r.Tokens, &r.Turns, &r.DurationS,
			&r.ToolCalls, &r.FileReads, &r.CostUSD, &r.JudgeScore, &valid, &at); err != nil {
			return nil, fmt.Errorf("telemetry: scan ab run: %w", err)
		}
		r.Valid = valid != 0
		r.ImportedAt = fromNanos(at)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Purge deletes calls, memory events and links older than before, then
// sessions that no longer have calls or memory events. It returns the number
// of rows removed from those tables.
func (s *SQLiteStore) Purge(ctx context.Context, before time.Time) (int64, error) {
	var removed int64
	err := s.write(ctx, func(q querier) error {
		b := toNanos(before)
		for _, stmt := range []string{
			`DELETE FROM calls WHERE ts < ?`,
			`DELETE FROM memory_events WHERE ts < ?`,
			`DELETE FROM session_links WHERE linked_at < ? OR call_id NOT IN (SELECT id FROM calls)`,
		} {
			res, err := q.ExecContext(ctx, stmt, b)
			if err != nil {
				return fmt.Errorf("telemetry: purge: %w", err)
			}
			n, _ := res.RowsAffected()
			removed += n
		}
		// A session that still has memory events stays, so its rows keep a
		// session to join to. The plan's rule is "no calls"; this keeps it.
		_, err := q.ExecContext(ctx, `DELETE FROM sessions
			WHERE id NOT IN (SELECT session_id FROM calls)
			  AND id NOT IN (SELECT session_id FROM memory_events)`)
		return err
	})
	return removed, err
}

// Stats reports sizes, row counts, the time range and dropped events.
func (s *SQLiteStore) Stats(ctx context.Context) (Stats, error) {
	st := Stats{Path: s.path}
	for _, p := range []string{s.path, s.path + "-wal"} {
		if fi, err := os.Stat(p); err == nil {
			st.SizeBytes += fi.Size()
		}
	}
	counts := []struct {
		q   string
		dst *int64
	}{
		{`SELECT COUNT(*) FROM calls`, &st.Calls},
		{`SELECT COUNT(*) FROM memory_events`, &st.MemoryEvents},
		{`SELECT COUNT(*) FROM sessions`, &st.Sessions},
		{`SELECT COUNT(*) FROM session_links`, &st.Links},
	}
	for _, c := range counts {
		if err := s.db.QueryRowContext(ctx, c.q).Scan(c.dst); err != nil {
			return st, fmt.Errorf("telemetry: stats: %w", err)
		}
	}
	// The exact/heuristic split of the call rows.
	st.Correlation = map[string]int64{}
	rows, err := s.db.QueryContext(ctx, `SELECT correlation, COUNT(*) FROM calls GROUP BY correlation`)
	if err != nil {
		return st, fmt.Errorf("telemetry: stats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			key string
			n   int64
		)
		if err := rows.Scan(&key, &n); err != nil {
			return st, fmt.Errorf("telemetry: stats: %w", err)
		}
		if key == "" {
			key = CorrHeuristic // a row with no correlation is attributable to nobody
		}
		st.Correlation[key] += n
	}
	if err := rows.Err(); err != nil {
		return st, fmt.Errorf("telemetry: stats: %w", err)
	}
	var lo, hi sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MIN(ts), MAX(ts) FROM (
		SELECT ts FROM calls UNION ALL SELECT ts FROM memory_events)`).Scan(&lo, &hi); err != nil {
		return st, fmt.Errorf("telemetry: stats range: %w", err)
	}
	st.OldestTS, st.NewestTS = fromNanos(lo.Int64), fromNanos(hi.Int64)
	if !lo.Valid {
		st.OldestTS = time.Time{}
	}
	if !hi.Valid {
		st.NewestTS = time.Time{}
	}
	d, err := s.dropped(ctx)
	if err != nil {
		return st, fmt.Errorf("telemetry: stats dropped: %w", err)
	}
	st.DroppedEvents = d
	return st, nil
}
