package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/FreePeak/LeanKG/internal/errs"
)

// Error is a store failure: the database could not complete a read or write
// the caller asked for. It is a server fault, so transports answer it 500.
type Error struct {
	Op  string // the store operation that failed, e.g. "status"
	Err error  // the driver error, kept on the chain for errors.Is
}

func (e *Error) Error() string { return fmt.Sprintf("store %s: %v", e.Op, e.Err) }

func (e *Error) Unwrap() error { return e.Err }

// Fail classifies a failed store call for a transport. A cancelled call stays
// context.Canceled (not a data failure); a Postgres that cannot be reached is
// the PG_UNREACHABLE catalog error; any other failure is an *Error.
func Fail(op string, err error) error {
	if err == nil || errors.Is(err, context.Canceled) {
		return err
	}
	var connect *pgconn.ConnectError
	if errors.As(err, &connect) {
		return errs.NewError(errs.PGUnreachable, fmt.Sprintf("store %s: %v", op, err), "")
	}
	return &Error{Op: op, Err: err}
}
