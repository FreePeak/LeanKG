package rpc_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	connect "connectrpc.com/connect"

	"github.com/FreePeak/LeanKG/internal/core"
	leankgv1 "github.com/FreePeak/LeanKG/internal/rpc/leankg/v1"
	"github.com/FreePeak/LeanKG/internal/store"
)

// elementCountFails fails the one read the status path makes; the nil embedded
// interface panics on anything else, so the call must stop at the failed read.
type elementCountFails struct{ store.Backend }

func (elementCountFails) Path() string { return "" }

func (elementCountFails) ElementCount(context.Context) (int, error) {
	return 0, errors.New("no such table: elements")
}

// TestStatusStoreFailureHidesDriverText: a store fault on the Status RPC
// answers CodeInternal with no driver text (table and column names); the cause
// is logged instead.
func TestStatusStoreFailureHidesDriverText(t *testing.T) {
	client := serveRPC(t, core.New(elementCountFails{}, core.Options{}))
	_, err := client.Status(context.Background(), connect.NewRequest(&leankgv1.StatusRequest{}))
	if err == nil {
		t.Fatal("Query on a failing store returned no error")
	}
	if strings.Contains(err.Error(), "no such table") {
		t.Fatalf("RPC error leaks driver text: %s", err.Error())
	}
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code %v (%.200s); want %v for a store fault", connect.CodeOf(err), err.Error(), connect.CodeInternal)
	}
}
