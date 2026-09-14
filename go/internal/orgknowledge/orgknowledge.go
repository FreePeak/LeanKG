// Package orgknowledge is the org/ops knowledge surface of the Go engine: it
// ports the Rust CLI (`incident`, `note`, `env-conflicts`), the MCP tools
// (query_incidents, find_env_conflicts, get_service_context), the
// /api/v2/* reads and the db::* helpers behind them.
//
// Layout mirrors the Rust split: internal/store holds the typed tables
// (incidents, knowledge_entries, service_metadata, env_snapshots), this
// package holds the policy — incident validation, the note/annotation shape,
// conflict classification and the service-context aggregation.
//
// Behaviors carried over verbatim from the Rust reference are called out on
// each function; the two deliberate divergences are documented where they
// happen (deterministic ordering, and env_snapshots standing in for the Rust
// engine's env-scoped code_elements rows, see internal/store/schema.go
// migration 010).
package orgknowledge

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Knowledge is the org/ops knowledge surface over one project store.
type Knowledge struct {
	st  store.Backend
	now func() int64
}

// New binds the surface to a store backend.
func New(st store.Backend) *Knowledge {
	return &Knowledge{st: st, now: func() int64 { return time.Now().Unix() }}
}

// Store exposes the backend (callers that need raw table access, e.g. the
// status surface counting knowledge rows).
func (k *Knowledge) Store() store.Backend { return k.st }

// AuthorFromEnv is the Rust author default: $USER, else "unknown".
func AuthorFromEnv() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "unknown"
}

// NewID builds the Rust-shaped identifier for a record class: an uppercase
// prefix plus a random v4 UUID (Rust used uuid::Uuid::new_v4, e.g.
// "INC-9f1c...", "NOTE-9f1c...").
func NewID(prefix string) string { return prefix + newUUIDv4() }

// newUUIDv4 renders 16 random bytes as a v4 UUID string. crypto/rand.Read is
// documented never to fail (it panics internally on an unrecoverable error),
// so there is no error path to fake here.
func newUUIDv4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
