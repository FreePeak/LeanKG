package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
