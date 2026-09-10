package store

import (
	"context"
)

// OpenPG is the PostgreSQL+pgvector backend entrypoint (W4). Until the
// implementation lands, OpenBackend reports a clear error instead of
// pretending; the DSN is accepted and redacted in the message.
func OpenPG(ctx context.Context, pgURL, projectDir string, mode Mode) (Backend, error) {
	_ = ctx
	_ = projectDir
	_ = mode
	return nil, fmtPGNotReady(pgURL)
}
