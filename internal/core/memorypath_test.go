package core

import (
	"context"
	"strings"
	"testing"
)

// TestMemoryCurationAcceptsEitherPathOrFile pins ONE rule across every memory
// write command: a curation call names its target file, and the engine reads
// that name from whichever of the two advertised fields carries it.
//
// The defect, found by driving the WRITE side over stdio (waves 1-5 only ever
// read memory). The import schema advertises the target under two names:
// `path` for create/str_replace/insert/delete/rename, and `file` for
// add/replace/remove. `withFlatArgs` folds the flat fields into Args, but it
// folds `file` and NOT `path` — so the two families of commands read the
// target from opposite fields, and an agent that used the other one got:
//
//	import action=memory command=add path=MEMORY.md text="..."
//	  -> memory: invalid memory path: empty path
//
// while the identical call with `file=MEMORY.md` worked. Same engine, same
// target, opposite outcome depending on which half of the schema the agent
// happened to read — and the error names an empty path the agent never sent,
// which reads like a bug in the engine rather than a field-name mismatch.
//
// The fix is the whole point of `withFlatArgs`: it exists because a schema-
// shaped call must not silently lose content, and the same reasoning applies
// to the target. One lookup, either name, and the two field names stop being a
// trap.
func TestMemoryCurationAcceptsEitherPathOrFile(t *testing.T) {
	e, mem := newEngine(t)
	ctx := context.Background()

	seed := "import action=memory command=create path=MEMORY.md content=\"# Memory\\n\\nfirst line\\n\""
	if _, err := e.Import(ctx, ImportRequest{
		Action: "memory", Command: "create", Path: "MEMORY.md",
		Content: "# Memory\n\nfirst line\n",
	}); err != nil {
		t.Fatalf("%s: %v", seed, err)
	}

	// Every command that takes a target, called the way the OTHER half of the
	// schema names it. `add` APPENDS ("§ <text>"), so the edit commands below
	// seed the exact string they then replace or remove.
	seedWrite := func(cmd, text string) {
		t.Helper()
		if _, err := e.Import(ctx, ImportRequest{Action: "memory", Command: "add", File: "MEMORY.md", Text: text}); err != nil {
			t.Fatalf("seed add %q: %v", text, err)
		}
	}
	seedWrite("via file", "KEEPME")
	seedWrite("via file too", "DROPME")

	cases := []struct {
		name string
		req  ImportRequest
	}{
		{"add via path (schema says file)", ImportRequest{Action: "memory", Command: "add", Path: "MEMORY.md", Text: "added by path"}},
		{"replace via path", ImportRequest{Action: "memory", Command: "replace", Path: "MEMORY.md", Old: "KEEPME", New: "REPLACED"}},
		{"replace via file", ImportRequest{Action: "memory", Command: "replace", File: "MEMORY.md", Old: "REPLACED", New: "KEEPME"}},
		{"remove via path", ImportRequest{Action: "memory", Command: "remove", Path: "MEMORY.md", Text: "DROPME"}},
		{"remove via file", ImportRequest{Action: "memory", Command: "remove", File: "MEMORY.md", Text: "added by path"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := e.Import(ctx, tc.req); err != nil {
				t.Fatalf("%s: %v (the engine must accept either advertised field for the target file)", tc.name, err)
			}
		})
	}

	// And the writes actually landed in ONE file — not two, not a new one.
	body, err := mem.View("MEMORY.md", 0)
	if err != nil {
		t.Fatalf("read back MEMORY.md: %v", err)
	}
	if !strings.Contains(string(body), "first line") {
		t.Errorf("MEMORY.md lost its seeded content: %q", body)
	}
	if !strings.Contains(body, "KEEPME") {
		t.Errorf("a replace lost the text it kept: %q", body)
	}
	if strings.Contains(body, "DROPME") || strings.Contains(body, "added by path") {
		t.Errorf("a remove did not remove: %q", body)
	}

	// An explicit args key still wins: withFlatArgs merges flat UNDER args, so
	// a caller that deliberately nests must not be overridden by a stale flat
	// field. That precedence is pre-existing and pinned here because the fix
	// touches the same merge.
	_, err = e.Import(ctx, ImportRequest{
		Action: "memory", Command: "add", File: "MEMORY.md", Text: "flat text",
		Args: map[string]any{"file": "USER.md"},
	})
	if err != nil {
		t.Fatalf("args-nested add: %v", err)
	}
	user, err := mem.View("USER.md", 0)
	if err != nil {
		t.Fatalf("read back USER.md: %v", err)
	}
	if !strings.Contains(user, "flat text") {
		t.Errorf("an explicit args.file must win over the flat field; USER.md = %q", user)
	}
}
