package core

import (
	"errors"
	"testing"
)

// CX-45: the LSP pool is created once, shared, and shut down by Close; Close is
// idempotent, and an lsp query after Close is refused rather than starting a
// new pool nobody will close.
func TestEngineClosePoolLifecycle(t *testing.T) {
	e := New(seededStore(t), Options{})
	first, err := e.lspPool()
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.lspPool()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("lspPool must create the pool once and share it")
	}
	e.Close()
	e.Close()
	if _, err := e.lspPool(); !errors.Is(err, errEngineClosed) {
		t.Fatalf("lspPool after Close = %v, want errEngineClosed", err)
	}
}

// An engine that never ran an lsp query has no pool to release; Close is still
// safe to call.
func TestEngineCloseWithoutPool(t *testing.T) {
	e := New(seededStore(t), Options{})
	e.Close()
}
