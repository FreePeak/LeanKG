package lsp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/langs"
)

// fakeLSP is a POSIX-sh LSP server: it answers the handshake, serves canned
// symbol payloads, and can be told to go mute on documentSymbol requests.
// Behaviour is selected with FAKE_LSP_MODE: hier (default) | flat | silent |
// rich. rich mode answers per-URI documentSymbol payloads (with detail +
// hierarchy) and hover docs, which the enrichment tests consume.
const fakeLSP = `#!/bin/sh
mode="${FAKE_LSP_MODE:-hier}"
cr=$(printf '\r')

send() {
  n=$(printf '%s' "$1" | wc -c | tr -d ' ')
  printf 'Content-Length: %s\r\n\r\n%s' "$n" "$1"
}

while :; do
  clen=""
  while IFS= read -r line; do
    line="${line%"$cr"}"
    [ -z "$line" ] && break
    case "$line" in
      [Cc]ontent-[Ll]ength:*) clen=$(printf '%s' "${line#*:}" | tr -d ' \r') ;;
    esac
  done
  [ -n "$clen" ] || exit 0
  body=$(dd bs=1 count="$clen" 2>/dev/null)
  [ -n "$body" ] || exit 0
  id=$(printf '%s' "$body" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  [ -n "$id" ] || continue
  method=$(printf '%s' "$body" | sed -n 's/.*"method":"\([^"]*\)".*/\1/p')
  case "$method" in
    initialize)
      send "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"capabilities\":{\"textDocumentSymbolProvider\":true}}}"
      ;;
    shutdown)
      send "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":null}"
      ;;
    textDocument/hover)
      send "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"contents\":{\"kind\":\"markdown\",\"value\":\"server hover documentation\"}}}"
      ;;
    textDocument/documentSymbol)
      if [ "$mode" = silent ]; then
        sleep 60
        continue
      fi
      if [ "$mode" = flat ]; then
        send "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":[{\"name\":\"flatFn\",\"kind\":12,\"location\":{\"range\":{\"start\":{\"line\":4},\"end\":{\"line\":8}}}}]}"
      elif [ "$mode" = rich ]; then
        case "$body" in
          *a.go*)
            send "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":[{\"name\":\"Handle\",\"kind\":12,\"detail\":\"func Handle(x int) error\",\"range\":{\"start\":{\"line\":2},\"end\":{\"line\":6}}},{\"name\":\"Server\",\"kind\":5,\"range\":{\"start\":{\"line\":8},\"end\":{\"line\":20}},\"children\":[{\"name\":\"Serve\",\"kind\":6,\"range\":{\"start\":{\"line\":10},\"end\":{\"line\":16}}}]},{\"name\":\"Config\",\"kind\":23,\"range\":{\"start\":{\"line\":30},\"end\":{\"line\":34}}}]}"
            ;;
          *)
            send "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":[{\"name\":\"Helper2\",\"kind\":12,\"detail\":\"func Helper2()\",\"range\":{\"start\":{\"line\":10},\"end\":{\"line\":12}}}]}"
            ;;
        esac
      else
        send "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":[{\"name\":\"Outer\",\"kind\":5,\"range\":{\"start\":{\"line\":2},\"end\":{\"line\":20}},\"children\":[{\"name\":\"inner\",\"kind\":12,\"range\":{\"start\":{\"line\":6},\"end\":{\"line\":9}}}]}]}"
      fi
      ;;
    workspace/symbol)
      send "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":[{\"name\":\"WsSym\",\"kind\":6,\"location\":{\"range\":{\"start\":{\"line\":10},\"end\":{\"line\":12}}}}]}"
      ;;
    *)
      send "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":null}"
      ;;
  esac
done
`

// fakeSpec drops a POSIX-sh fake server on PATH and returns a spec that
// resolves to it. The caller's t.Setenv-scoped PATH is inherited because the
// manager never overrides the child environment.
func fakeSpec(t *testing.T, mode string) *langs.LSPSpec {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake LSP server is a POSIX sh script")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-lsp")
	if err := os.WriteFile(script, []byte(fakeLSP), 0o755); err != nil {
		t.Fatalf("write fake server: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_LSP_MODE", mode)
	return &langs.LSPSpec{Commands: []string{"fake-lsp"}}
}

func TestFramingRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	for _, payload := range []string{`{"jsonrpc":"2.0"}`, `{"a":1,"b":[2,3]}`} {
		if err := writeFrame(w, []byte(payload)); err != nil {
			t.Fatalf("writeFrame: %v", err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	r := bufio.NewReader(&buf)
	for _, want := range []string{`{"jsonrpc":"2.0"}`, `{"a":1,"b":[2,3]}`} {
		got, err := readFrame(r)
		if err != nil {
			t.Fatalf("readFrame: %v", err)
		}
		if string(got) != want {
			t.Errorf("readFrame = %q, want %q", got, want)
		}
	}
	if _, err := readFrame(r); !errors.Is(err, io.EOF) {
		t.Errorf("readFrame at end = %v, want EOF", err)
	}

	if _, err := readFrame(bufio.NewReader(strings.NewReader("Content-Length: x\r\n\r\n"))); !errors.Is(err, ErrProtocol) {
		t.Errorf("bad Content-Length = %v, want ErrProtocol", err)
	}
	if _, err := readFrame(bufio.NewReader(strings.NewReader("X-Other: 1\r\n\r\nbody"))); !errors.Is(err, ErrProtocol) {
		t.Errorf("missing Content-Length = %v, want ErrProtocol", err)
	}
}

func TestStartHandshakeAndClose(t *testing.T) {
	spec := fakeSpec(t, "hier")
	root := t.TempDir()

	if !Available(spec) {
		t.Fatal("Available(spec) = false, want true")
	}
	if Available(&langs.LSPSpec{Commands: []string{"nope-not-here-xyz"}}) {
		t.Fatal("Available = true for unresolvable command")
	}
	if Available(nil) {
		t.Fatal("Available(nil) = true")
	}

	ctx := context.Background()
	c, err := Start(ctx, langs.Go, spec, root)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if c.Root() != root {
		t.Errorf("Root = %q, want %q", c.Root(), root)
	}
	if c.Language() != langs.Go {
		t.Errorf("Language = %q, want %q", c.Language(), langs.Go)
	}
	if idle := c.IdleFor(); idle < 0 || idle > 5*time.Second {
		t.Errorf("IdleFor = %v, want a fresh client", idle)
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	// Requests after teardown must fail fast, not hang.
	if _, err := c.WorkspaceSymbols(ctx, "x"); err == nil {
		t.Error("WorkspaceSymbols after Close = nil error, want failure")
	}
}

func TestDocumentSymbolsHierarchical(t *testing.T) {
	spec := fakeSpec(t, "hier")
	root := t.TempDir()
	file := filepath.Join(root, "a.go")
	if err := os.WriteFile(file, []byte("package a\n\nfunc f() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := Start(context.Background(), langs.Go, spec, root)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Close()

	syms, err := c.DocumentSymbols(context.Background(), file)
	if err != nil {
		t.Fatalf("DocumentSymbols: %v", err)
	}
	// Child is flattened after its parent; 0-based server lines become 1-based.
	want := []Symbol{
		{Name: "Outer", Kind: 5, StartLine: 3, EndLine: 21},
		{Name: "inner", Kind: 12, StartLine: 7, EndLine: 10},
	}
	if len(syms) != len(want) {
		t.Fatalf("got %d symbols %v, want %d", len(syms), syms, len(want))
	}
	for i, w := range want {
		if syms[i] != w {
			t.Errorf("symbol %d = %+v, want %+v", i, syms[i], w)
		}
	}
}

func TestDocumentSymbolsFlat(t *testing.T) {
	spec := fakeSpec(t, "flat")
	root := t.TempDir()
	file := filepath.Join(root, "a.go")
	if err := os.WriteFile(file, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := Start(context.Background(), langs.Go, spec, root)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Close()

	syms, err := c.DocumentSymbols(context.Background(), file)
	if err != nil {
		t.Fatalf("DocumentSymbols: %v", err)
	}
	want := Symbol{Name: "flatFn", Kind: 12, StartLine: 5, EndLine: 9}
	if len(syms) != 1 || syms[0] != want {
		t.Fatalf("symbols = %v, want [%+v]", syms, want)
	}
}

func TestWorkspaceSymbols(t *testing.T) {
	spec := fakeSpec(t, "hier")
	root := t.TempDir()

	c, err := Start(context.Background(), langs.Go, spec, root)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Close()

	syms, err := c.WorkspaceSymbols(context.Background(), "thing")
	if err != nil {
		t.Fatalf("WorkspaceSymbols: %v", err)
	}
	want := Symbol{Name: "WsSym", Kind: 6, StartLine: 11, EndLine: 13}
	if len(syms) != 1 || syms[0] != want {
		t.Fatalf("symbols = %v, want [%+v]", syms, want)
	}
}

func TestErrNoServer(t *testing.T) {
	fakeSpec(t, "hier") // put fake-lsp on PATH so the negative case is about the command, not PATH
	spec := &langs.LSPSpec{Commands: []string{"definitely-not-a-real-binary-xyz"}}
	c, err := Start(context.Background(), langs.Go, spec, t.TempDir())
	if !errors.Is(err, ErrNoServer) {
		t.Fatalf("Start error = %v, want ErrNoServer", err)
	}
	if c != nil {
		t.Fatal("Start returned a client alongside the error")
	}
	if _, err := Start(context.Background(), langs.Go, nil, t.TempDir()); !errors.Is(err, ErrNoServer) {
		t.Errorf("Start(nil spec) = %v, want ErrNoServer", err)
	}
}

func TestErrTimeout(t *testing.T) {
	spec := fakeSpec(t, "silent")
	root := t.TempDir()
	file := filepath.Join(root, "a.go")
	if err := os.WriteFile(file, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := Start(context.Background(), langs.Go, spec, root)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = c.DocumentSymbols(ctx, file)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("DocumentSymbols error = %v, want ErrTimeout", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("DocumentSymbols took %v; ctx deadline ignored", elapsed)
	}
}

func TestManagerReuse(t *testing.T) {
	spec := fakeSpec(t, "hier")
	root := t.TempDir()
	m := NewManager(0)
	defer m.Shutdown()

	ctx := context.Background()
	first, err := m.Get(ctx, langs.Go, spec, root)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	second, err := m.Get(ctx, langs.Go, spec, root)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if first != second {
		t.Fatal("Get reused=false: two identical keys returned different clients")
	}
	if m.Len() != 1 {
		t.Errorf("Len = %d, want 1", m.Len())
	}
	// A different root is a different server.
	other, err := m.Get(ctx, langs.Go, spec, t.TempDir())
	if err != nil {
		t.Fatalf("Get(other root): %v", err)
	}
	if other == first {
		t.Fatal("Get returned the same client for a different root")
	}
	if m.Len() != 2 {
		t.Errorf("Len = %d, want 2", m.Len())
	}
	// unresolvable spec for a KEY with no pooled client must surface ErrNoServer
	// (a pooled client ignores spec — the agent's earlier test wrongly reused
	// the (go, root) key and thus got the live client, not a Start attempt).
	if _, err := m.Get(ctx, langs.Swift, &langs.LSPSpec{Commands: []string{"nope-xyz"}}, root); !errors.Is(err, ErrNoServer) {
		t.Errorf("Get with unresolvable spec = %v, want ErrNoServer", err)
	}
}

func TestManagerIdleEviction(t *testing.T) {
	spec := fakeSpec(t, "hier")
	root := t.TempDir()
	m := NewManager(50 * time.Millisecond)
	defer m.Shutdown()

	ctx := context.Background()
	first, err := m.Get(ctx, langs.Go, spec, root)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if m.Len() != 0 {
		t.Errorf("Len = %d after TTL elapsed, want 0", m.Len())
	}
	second, err := m.Get(ctx, langs.Go, spec, root)
	if err != nil {
		t.Fatalf("Get after eviction: %v", err)
	}
	if second == first {
		t.Fatal("idle client was not evicted")
	}
}
