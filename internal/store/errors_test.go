package store

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FreePeak/LeanKG/internal/errs"
)

func TestFailWrapsAStoreFailureAsAServerFault(t *testing.T) {
	cause := errors.New("no such table: elements")
	err := Fail("status", cause)
	var se *Error
	if !errors.As(err, &se) || se.Op != "status" {
		t.Fatalf("Fail = %v (%T); want a *store.Error for op status", err, err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("Fail = %v; the driver error must stay on the chain", err)
	}
}

func TestFailKeepsCancellationAndNilAsTheyAre(t *testing.T) {
	var se *Error
	if got := Fail("status", context.Canceled); !errors.Is(got, context.Canceled) || errors.As(got, &se) {
		t.Fatalf("cancelled call = %v; a cancellation is not a store failure", got)
	}
	if got := Fail("status", nil); got != nil {
		t.Fatalf("Fail(nil) = %v; want nil", got)
	}
}

// TestFailMapsAnUnreachablePostgresToPGUnreachable connects to a loopback port
// that was just closed, so the connect is refused at once and no server runs.
func TestFailMapsAnUnreachablePostgresToPGUnreachable(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	pool, err := pgxpool.New(context.Background(), "postgres://u:p@"+addr+"/db?connect_timeout=2")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	perr := pool.Ping(context.Background())
	if perr == nil {
		t.Fatal("ping of a closed port succeeded")
	}
	var ce *errs.Error
	got := Fail("status", perr)
	if !errors.As(got, &ce) || ce.Code() != errs.PGUnreachable.Code {
		t.Fatalf("Fail(connect refused) = %v; want code %s", got, errs.PGUnreachable.Code)
	}
}
