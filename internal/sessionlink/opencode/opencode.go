// Package opencode is the sessionlink adapter for the opencode SQLite store
// ($XDG_DATA_HOME/opencode/opencode.db, else ~/.local/share/opencode/opencode.db).
// It reads session, message and part rows. The database is opened read-only
// (mode=ro and query_only) and the adapter never writes to it.
package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/FreePeak/LeanKG/internal/sessionlink"
)

const clientName = "opencode"

// requiredColumns is the schema this adapter understands. A store that lacks
// any of them is an unsupported version, not an error to crash on.
var requiredColumns = map[string][]string{
	"session": {"id", "directory", "time_created", "time_updated"},
	"message": {"id", "session_id", "time_created", "data"},
	"part":    {"id", "message_id", "session_id", "time_created", "data"},
}

type adapter struct{}

// New returns the opencode adapter.
func New() sessionlink.Adapter { return adapter{} }

func (adapter) Client() string { return clientName }

func dbPath(home string) string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" && filepath.IsAbs(x) {
		return filepath.Join(x, "opencode", "opencode.db")
	}
	return filepath.Join(home, ".local", "share", "opencode", "opencode.db")
}

func (adapter) Detect(home string) bool {
	fi, err := os.Stat(dbPath(home))
	return err == nil && fi.Mode().IsRegular()
}

func (adapter) Roots(home string) []string { return []string{dbPath(home)} }

// openRO opens the store read-only and checks its schema.
func openRO(ctx context.Context, path string) (*sql.DB, error) {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return nil, sessionlink.ErrNotFound
	}
	u := url.URL{Scheme: "file", Path: path}
	dsn := u.String() + "?mode=ro&_pragma=query_only(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := checkSchema(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func checkSchema(ctx context.Context, db *sql.DB) error {
	for table, cols := range requiredColumns {
		rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
		if err != nil {
			return sessionlink.ErrUnsupportedVersion
		}
		have := map[string]bool{}
		for rows.Next() {
			var cid, notnull, pk int
			var name, typ string
			var dflt sql.NullString
			if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
				rows.Close()
				return sessionlink.ErrUnsupportedVersion
			}
			have[name] = true
		}
		rows.Close()
		for _, c := range cols {
			if !have[c] {
				return sessionlink.ErrUnsupportedVersion
			}
		}
	}
	return nil
}

// Locate finds the session for c. An exact match needs the client session id
// to name an opencode session that holds the call. The heuristic needs a
// session in the call's directory whose time range covers the call, and a
// tool part with the same canonical tool and args hash near the call time.
func (adapter) Locate(ctx context.Context, home string, c sessionlink.CallRef, rd *sessionlink.Reader) (sessionlink.TranscriptRef, sessionlink.Confidence, error) {
	if err := ctx.Err(); err != nil {
		return sessionlink.TranscriptRef{}, 0, err
	}
	path := dbPath(home)
	db, err := openRO(ctx, path)
	if err != nil {
		return sessionlink.TranscriptRef{}, 0, err
	}
	defer db.Close()

	if c.ClientSessionID != "" {
		evs, err := sessionEvents(ctx, rd, db, path, c.ClientSessionID)
		if err != nil {
			return sessionlink.TranscriptRef{}, 0, err
		}
		if sessionlink.CountMatchesNear(evs, c) > 0 {
			return sessionlink.TranscriptRef{Client: clientName, Path: path, SessionID: c.ClientSessionID}, sessionlink.ConfidenceExact, nil
		}
		return sessionlink.TranscriptRef{}, 0, sessionlink.ErrNotFound
	}

	if c.Cwd == "" || c.Tool == "" {
		return sessionlink.TranscriptRef{}, 0, sessionlink.ErrNotFound
	}
	ids, err := coveringSessions(ctx, db, c)
	if err != nil {
		return sessionlink.TranscriptRef{}, 0, err
	}
	var total int
	var first string
	for _, id := range ids {
		evs, err := sessionEvents(ctx, rd, db, path, id)
		if err != nil {
			return sessionlink.TranscriptRef{}, 0, err
		}
		n := sessionlink.CountMatchesNear(evs, c)
		if n == 0 {
			continue
		}
		if first == "" {
			first = id
		}
		total += n
	}
	if first == "" {
		return sessionlink.TranscriptRef{}, 0, sessionlink.ErrNotFound
	}
	return sessionlink.TranscriptRef{Client: clientName, Path: path, SessionID: first},
		sessionlink.ConfidenceFor(total), nil
}

// coveringSessions lists sessions in the call's directory whose time range
// covers the call, newest first.
func coveringSessions(ctx context.Context, db *sql.DB, c sessionlink.CallRef) ([]string, error) {
	q := `SELECT id FROM session WHERE directory = ?`
	args := []any{c.Cwd}
	if !c.TS.IsZero() {
		q += ` AND time_created <= ? AND time_updated >= ?`
		args = append(args, c.TS.UnixMilli(), c.TS.UnixMilli())
	}
	q += ` ORDER BY time_updated DESC`
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Window extracts up to n turns before and after the matched call.
func (adapter) Window(ctx context.Context, t sessionlink.TranscriptRef, c sessionlink.CallRef, n int, rd *sessionlink.Reader) (sessionlink.Window, error) {
	if err := ctx.Err(); err != nil {
		return sessionlink.Window{}, err
	}
	db, err := openRO(ctx, t.Path)
	if err != nil {
		return sessionlink.Window{}, err
	}
	defer db.Close()
	evs, err := sessionEvents(ctx, rd, db, t.Path, t.SessionID)
	if err != nil {
		return sessionlink.Window{}, err
	}
	conf := sessionlink.ConfidenceExact
	if c.ClientSessionID == "" || t.SessionID != c.ClientSessionID {
		conf = sessionlink.ConfidenceFor(sessionlink.CountMatchesNear(evs, c))
	}
	return sessionlink.BuildWindow(clientName, t.SessionID, t.Path, evs, c, n, conf)
}

// Transcript returns the session's turns, capped at maxTurns.
func (adapter) Transcript(ctx context.Context, t sessionlink.TranscriptRef, maxTurns int) ([]sessionlink.Turn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db, err := openRO(ctx, t.Path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	evs, err := sessionEvents(ctx, nil, db, t.Path, t.SessionID)
	if err != nil {
		return nil, err
	}
	return sessionlink.TurnsOf(evs, maxTurns), nil
}

type msgData struct {
	Role    string `json:"role"`
	ModelID string `json:"modelID"`
	Time    struct {
		Created int64 `json:"created"`
	} `json:"time"`
	Tokens struct {
		Input  int64 `json:"input"`
		Output int64 `json:"output"`
		Cache  struct {
			Read  int64 `json:"read"`
			Write int64 `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
}

type partData struct {
	Type   string `json:"type"`
	Text   string `json:"text"`
	Tool   string `json:"tool"`
	CallID string `json:"callID"`
	State  struct {
		Status string          `json:"status"`
		Input  json.RawMessage `json:"input"`
		Output string          `json:"output"`
		Error  json.RawMessage `json:"error"`
	} `json:"state"`
}

type partRow struct {
	createdMS int64
	data      partData
}

// sessionEvents returns one session's events, reading the session at most once
// per Reader. A database holds many sessions, so the key is path and session id.
func sessionEvents(ctx context.Context, rd *sessionlink.Reader, db *sql.DB, path, sessionID string) ([]sessionlink.Event, error) {
	return sessionlink.Parsed(rd, path+"\x00"+sessionID, func() ([]sessionlink.Event, error) {
		return loadEvents(ctx, db, sessionID)
	})
}

// loadEvents reads one session into adapter-neutral events, messages in
// order and parts in order within each message.
func loadEvents(ctx context.Context, db *sql.DB, sessionID string) ([]sessionlink.Event, error) {
	parts := map[string][]partRow{}
	prows, err := db.QueryContext(ctx, `SELECT id, message_id, time_created, data FROM part WHERE session_id = ? ORDER BY time_created, id`, sessionID)
	if err != nil {
		return nil, err
	}
	for prows.Next() {
		var msgID, raw string
		var created int64
		var id string
		if err := prows.Scan(&id, &msgID, &created, &raw); err != nil {
			prows.Close()
			return nil, err
		}
		var d partData
		if json.Unmarshal([]byte(raw), &d) != nil {
			continue // a malformed part is skipped, not fatal
		}
		parts[msgID] = append(parts[msgID], partRow{createdMS: created, data: d})
	}
	prows.Close()
	if err := prows.Err(); err != nil {
		return nil, err
	}

	mrows, err := db.QueryContext(ctx, `SELECT id, time_created, data FROM message WHERE session_id = ? ORDER BY time_created, id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer mrows.Close()
	var events []sessionlink.Event
	for mrows.Next() {
		var id, raw string
		var created int64
		if err := mrows.Scan(&id, &created, &raw); err != nil {
			return nil, err
		}
		var m msgData
		if json.Unmarshal([]byte(raw), &m) != nil {
			continue
		}
		ms := m.Time.Created
		if ms == 0 {
			ms = created
		}
		ts := time.UnixMilli(ms)
		switch m.Role {
		case "user":
			var text []string
			for _, p := range parts[id] {
				if p.data.Type == "text" && p.data.Text != "" {
					text = append(text, p.data.Text)
				}
			}
			if len(text) == 0 {
				continue
			}
			events = append(events, sessionlink.Event{
				Prompt: true,
				Turn:   sessionlink.Turn{Role: "user", Text: sessionlink.Clip(strings.Join(text, "\n"), sessionlink.TextCap), TS: ts},
			})
		case "assistant":
			turn := sessionlink.Turn{
				Role:         "assistant",
				TS:           ts,
				InputTokens:  m.Tokens.Input,
				OutputTokens: m.Tokens.Output,
				CacheRead:    m.Tokens.Cache.Read,
				CacheWrite:   m.Tokens.Cache.Write,
				Model:        m.ModelID,
			}
			var text []string
			for _, p := range parts[id] {
				switch p.data.Type {
				case "text":
					if p.data.Text != "" {
						text = append(text, p.data.Text)
					}
				case "tool":
					pts := time.UnixMilli(p.createdMS)
					tc := sessionlink.MakeToolCall(p.data.CallID, p.data.Tool, rawOr(p.data.State.Input), pts)
					if p.data.State.Output != "" || len(p.data.State.Error) > 0 {
						out := p.data.State.Output
						if out == "" {
							out = string(p.data.State.Error)
						}
						sessionlink.SetResult(&tc, out, p.data.State.Status == "error" || len(p.data.State.Error) > 0)
					}
					turn.ToolCalls = append(turn.ToolCalls, tc)
				}
			}
			turn.Text = sessionlink.Clip(strings.Join(text, "\n"), sessionlink.TextCap)
			events = append(events, sessionlink.Event{Turn: turn})
		}
	}
	return events, mrows.Err()
}

func rawOr(r json.RawMessage) []byte {
	if len(r) == 0 {
		return []byte("{}")
	}
	return r
}
