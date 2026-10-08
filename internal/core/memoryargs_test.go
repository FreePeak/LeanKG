package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/session"
)

// The import tool's advertised MCP input schema publishes the curation fields
// (content/old/new/text/file/insert_line) as TOP-LEVEL properties, so an agent
// following the schema sends them flat. The Engine read them only from
// req.Args, so a schema-shaped create reported {"command":"create","ok":true}
// and wrote an EMPTY file — a silent memory loss that only an xdev-session
// probe (an agent writing memory through the real tool) ever exercised.
func TestMemoryWriteFlatSchemaFieldsLandOnDisk(t *testing.T) {
	e, mem := newEngine(t)
	ctx := context.Background()

	out, err := e.Import(ctx, ImportRequest{
		Action:  "memory",
		Command: "create",
		Path:    "topics/flat.md",
		Content: "flat top-level content",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if ok, _ := out["ok"].(bool); !ok {
		t.Fatalf("create did not report ok: %#v", out)
	}
	data, err := os.ReadFile(filepath.Join(mem.Root(), "topics", "flat.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "flat top-level content" {
		t.Fatalf("file = %q, want %q (empty file = the silent-loss bug)", got, "flat top-level content")
	}

	// add/replace/remove carry `file` + `text` flat; str_replace carries old/new.
	for _, step := range []struct {
		req      ImportRequest
		file     string
		wantSubs []string
		absent   []string
	}{
		{
			req:      ImportRequest{Action: "memory", Command: "add", File: "MEMORY.md", Text: "- flat line"},
			file:     "MEMORY.md",
			wantSubs: []string{"- flat line"},
		},
		{
			req:      ImportRequest{Action: "memory", Command: "str_replace", Path: "topics/flat.md", Old: "flat", New: "flat-patched"},
			file:     "topics/flat.md",
			wantSubs: []string{"flat-patched"},
			absent:   []string{"flat top-level"},
		},
	} {
		if _, err := e.Import(ctx, step.req); err != nil {
			t.Fatalf("%s: %v", step.req.Command, err)
		}
		got, err := os.ReadFile(filepath.Join(mem.Root(), filepath.FromSlash(step.file)))
		if err != nil {
			t.Fatal(err)
		}
		for _, sub := range step.wantSubs {
			if !strings.Contains(string(got), sub) {
				t.Fatalf("%s: %s lacks %q: %q", step.req.Command, step.file, sub, got)
			}
		}
		for _, sub := range step.absent {
			if strings.Contains(string(got), sub) {
				t.Fatalf("%s: %s still carries %q: %q", step.req.Command, step.file, sub, got)
			}
		}
	}
}

// args-shaped callers must keep working, and an explicit args key wins over the
// flat one (never silently overridden).
func TestMemoryWriteArgsShapeStillWorksAndArgsWins(t *testing.T) {
	e, mem := newEngine(t)
	ctx := context.Background()

	if _, err := e.Import(ctx, ImportRequest{
		Action:  "memory",
		Command: "create",
		Path:    "topics/nested.md",
		Args:    map[string]any{"path": "topics/nested.md", "content": "nested content"},
	}); err != nil {
		t.Fatalf("args-shaped create: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(mem.Root(), "topics", "nested.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "nested content" {
		t.Fatalf("args-shaped write = %q, want %q", got, "nested content")
	}

	// Flat content + args content: args is the more explicit shape and wins.
	if _, err := e.Import(ctx, ImportRequest{
		Action:  "memory",
		Command: "create",
		Path:    "topics/precedence.md",
		Content: "flat loses",
		Args:    map[string]any{"content": "args wins"},
	}); err != nil {
		t.Fatalf("precedence create: %v", err)
	}
	got, err = os.ReadFile(filepath.Join(mem.Root(), "topics", "precedence.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "args wins" {
		t.Fatalf("precedence = %q, want %q (args must win over flat)", got, "args wins")
	}
}

// TestMemoryRenameTopLevelNewPath pins RS-06: the advertised schema puts
// new_path at the top level; it used to be dropped ("empty path").
func TestMemoryRenameTopLevelNewPath(t *testing.T) {
	e, _ := newEngine(t)
	if _, err := e.Import(context.Background(), ImportRequest{Action: "memory", Command: "create", Path: "topics/a.md", Content: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Import(context.Background(), ImportRequest{Action: "memory", Command: "rename", Path: "topics/a.md", NewPath: "topics/b.md"}); err != nil {
		t.Fatalf("rename with top-level new_path: %v", err)
	}
}

// TestMemoryViewAndSnapshotOverQuery pins RS-21: an MCP agent can read a
// memory file and the core snapshot through the query tool.
func TestMemoryViewAndSnapshotOverQuery(t *testing.T) {
	e, _ := newEngine(t)
	ctx := context.Background()
	if _, err := e.Import(ctx, ImportRequest{Action: "memory", Command: "create", Path: "topics/n.md", Content: "note body"}); err != nil {
		t.Fatal(err)
	}
	out, err := e.Query(ctx, QueryRequest{Action: "memory", Args: map[string]any{"command": "view", "path": "topics/n.md"}})
	if err != nil || out["content"] != "note body" {
		t.Fatalf("view: %v %v", out, err)
	}
	out, err = e.Query(ctx, QueryRequest{Action: "memory", Args: map[string]any{"command": "snapshot"}})
	if err != nil || out["command"] != "snapshot" {
		t.Fatalf("snapshot: %v %v", out, err)
	}
	if _, err := e.Query(ctx, QueryRequest{Action: "memory", Args: map[string]any{"command": "view", "path": "../../etc/hosts"}}); err == nil {
		t.Fatal("view escaped the memory root")
	}
}

// TestErrorsAndRefsAreProjectRelative pins RS-23: no client-facing error or
// session ref carries the server's absolute project path.
func TestErrorsAndRefsAreProjectRelative(t *testing.T) {
	e, _ := newEngine(t)
	proj := t.TempDir()
	e.SetProjectDir(proj)
	ctx := context.Background()
	_, err := e.Import(ctx, ImportRequest{Action: "memory", Command: "delete", Path: "topics/missing.md"})
	if err == nil {
		t.Fatal("deleting a missing memory file succeeded")
	}
	if root := e.projectRoot(); strings.Contains(err.Error(), root) || strings.Contains(err.Error(), proj) {
		t.Fatalf("error leaks the project path: %v", err)
	}
	out, err := e.Import(ctx, ImportRequest{Action: "session", Command: "offload",
		Args: map[string]any{"session_id": "s1", "node_id": "node-1", "payload": "p", "summary": "s"}})
	if err != nil {
		t.Fatal(err)
	}
	if p := out["offloaded"].(session.Ref).Path; filepath.IsAbs(p) {
		t.Fatalf("offload ref path is absolute: %s", p)
	}
}

// TestMemoryCreateReportsOverwrite: create still overwrites (memory-tool
// contract) but says so.
func TestMemoryCreateReportsOverwrite(t *testing.T) {
	e, _ := newEngine(t)
	ctx := context.Background()
	out, err := e.Import(ctx, ImportRequest{Action: "memory", Command: "create", Path: "topics/o.md", Content: "one"})
	if err != nil || out["overwrote"] != false {
		t.Fatalf("first create: %v %v", out, err)
	}
	out, err = e.Import(ctx, ImportRequest{Action: "memory", Command: "create", Path: "topics/o.md", Content: "two"})
	if err != nil || out["overwrote"] != true {
		t.Fatalf("second create must report overwrote=true: %v %v", out, err)
	}
}
