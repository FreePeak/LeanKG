package core

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/errs"
	"github.com/FreePeak/LeanKG/internal/httpx"
	"github.com/FreePeak/LeanKG/internal/store"
)

// openReadOnlyStore creates a migrated store at dir and reopens it read-only,
// the way a routed project's store is opened.
func openReadOnlyStore(t *testing.T, dir string) store.Backend {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(dir, ".leankg", "leankg.db")
	rw, err := store.OpenBackend(ctx, store.OpenOptions{DBPath: path, Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	if err := rw.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rw.Close(); err != nil {
		t.Fatal(err)
	}
	ro, err := store.OpenBackend(ctx, store.OpenOptions{DBPath: path, Mode: store.RO})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ro.Close() })
	return ro
}

// TestHostedRoutedStoreRefusesGraphImportWithATypedError: in hosted mode a
// graph-writing import on a routed (read-only) project answers a typed refusal
// that names the cause and the writer. It is not the raw SQLite text, and it
// keeps the 400 status a bad request has.
func TestHostedRoutedStoreRefusesGraphImportWithATypedError(t *testing.T) {
	dir := t.TempDir()
	ro := openReadOnlyStore(t, dir)

	_, err := New(ro, Options{ProjectDir: dir, Hosted: true}).Import(context.Background(), ImportRequest{Action: "docs", Path: dir})
	var coded *errs.Error
	if !errors.As(err, &coded) || coded.Code() != errs.RoutedReadOnly.Code {
		t.Fatalf("hosted graph import on a routed store = %v; want the %s refusal", err, errs.RoutedReadOnly.Code)
	}
	if strings.Contains(err.Error(), "readonly database") {
		t.Fatalf("refusal leaks the raw SQLite text: %v", err)
	}
	if !strings.Contains(err.Error(), "read-only in hosted mode") || !strings.Contains(err.Error(), "writer") {
		t.Fatalf("refusal must name the read-only routed project and the writer, got: %v", err)
	}
	if got := httpx.StatusFor(err); got != http.StatusBadRequest {
		t.Fatalf("status for the routed refusal = %d, want 400", got)
	}
}

// TestReadOnlyImportOutsideHostedKeepsItsOwnError: outside hosted mode a
// read-only store is not the routed refusal; the write reports its own error.
func TestReadOnlyImportOutsideHostedKeepsItsOwnError(t *testing.T) {
	dir := t.TempDir()
	ro := openReadOnlyStore(t, dir)

	_, err := New(ro, Options{ProjectDir: dir}).Import(context.Background(), ImportRequest{Action: "docs", Path: dir})
	var coded *errs.Error
	if errors.As(err, &coded) && coded.Code() == errs.RoutedReadOnly.Code {
		t.Fatalf("a non-hosted read-only import got the hosted refusal: %v", err)
	}
}
