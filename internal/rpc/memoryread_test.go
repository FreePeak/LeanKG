package rpc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	leankgv1 "github.com/FreePeak/LeanKG/internal/rpc/leankg/v1"
	"github.com/FreePeak/LeanKG/internal/rpc/leankg/v1/leankgv1connect"

	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/store"
)

// memoryReadService starts the ConnectRPC service over a real engine and
// returns a client bound to it (same construction as capture_test.go).
func memoryReadService(t *testing.T) leankgv1connect.LeanKGClient {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	mem, err := memory.Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	path, handler := leankgv1connect.NewLeanKGHandler(NewLeanKGService(core.New(st, mem, nil)))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return leankgv1connect.NewLeanKGClient(srv.Client(), srv.URL)
}

// TestMemoryReadAcceptsTheSameVocabularyAsMCP pins that the ConnectRPC wire
// answers the SAME memory-read commands as the MCP tool and the CLI.
//
// Measured live before this: the RPC refused `session_recall` with "valid:
// view, snapshot, search" while MCP accepted "session_recall, memories, view,
// snapshot, search" — because the RPC called engine.MemoryRead directly and
// skipped the dispatcher that knows the agent-facing aliases. A client on the
// second wire therefore could not do the one memory read the agent protocol
// names ("at session start call session_recall").
func TestMemoryReadAcceptsTheSameVocabularyAsMCP(t *testing.T) {
	c := memoryReadService(t)
	for _, tc := range []struct{ cmd, query string }{
		{"session_recall", "corpus"},
		{"memories", "corpus"},
		{"snapshot", ""},
		{"search", "corpus"},
		// view reads one memory FILE, so the term is a path, not a query — the
		// same requirement MCP has (the dispatcher falls back to query only
		// when args.path is empty, and a non-path term must be refused).
		{"view", "MEMORY.md"},
	} {
		_, err := c.MemoryRead(context.Background(),
			connect.NewRequest(&leankgv1.MemoryReadRequest{Command: tc.cmd, Query: tc.query}))
		if err != nil {
			t.Errorf("command %q refused on the RPC wire: %v", tc.cmd, err)
		}
	}
}

// TestMemoryReadUnknownCommandNamesTheRealVocabulary pins the refusal text:
// a client that mistypes must be told the vocabulary the OTHER surfaces list,
// not this wire's subset (the message is what a caller copies).
func TestMemoryReadUnknownCommandNamesTheRealVocabulary(t *testing.T) {
	c := memoryReadService(t)
	_, err := c.MemoryRead(context.Background(),
		connect.NewRequest(&leankgv1.MemoryReadRequest{Command: "zzz"}))
	if err == nil {
		t.Fatal("an unknown command was accepted")
	}
	if !contains(err.Error(), "session_recall") {
		t.Errorf("the refusal does not name session_recall: %q", err.Error())
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
