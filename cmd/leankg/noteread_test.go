package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/orgknowledge"
	"github.com/FreePeak/LeanKG/internal/store"
)

// TestNoteReadRoundTrip pins that a team note is not write-only.
//
// Measured on the live self-host 2026-10-11: `leankg note --target leankg
// --content …` wrote a knowledge_entries row (NOTE-8ac3…, tags "note") and NO
// surface could read it back — `note` had no read subcommand, service-context
// and team-map do not carry notes, there is no MCP notes action, and no
// /api/v2 notes route. Three Rust read ports existed in Go
// (KnowledgeEntriesByElement/ByFeature/ByEnvironment, reached via
// orgknowledge.NotesFor and friends) with ZERO production callers — their only
// callers were tests. Same write-only shape #470 fixed for reflect and #507
// for lessons.
func TestNoteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := store.OpenBackend(context.Background(), dir, store.EngineSQLite, "", "", store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	k := orgknowledge.New(st)
	const target = "pkg.Service.Handle"
	if _, err := k.AddNote(target, "rotate the key before shipping", "local", "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := k.AddNote("pkg.Other", "unrelated note", "local", "tester"); err != nil {
		t.Fatal(err)
	}

	// The reader the CLI now calls.
	notes, err := k.NotesFor(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 {
		t.Fatalf("NotesFor(%s) = %d notes, want 1 (the other target must not leak in)", target, len(notes))
	}
	if !strings.Contains(notes[0].Content, "rotate the key") {
		t.Errorf("note content lost: %q", notes[0].Content)
	}
	// An unknown target is an honest empty, never an error.
	none, err := k.NotesFor("pkg.Nothing")
	if err != nil {
		t.Fatalf("unknown target errored: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("unknown target returned %d notes, want none", len(none))
	}
}

// TestNoteVerbListsWhenContentIsEmpty pins the CLI contract: no --content
// means READ, which is what the flat verb had no way to express.
func TestNoteVerbListsWhenContentIsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Seed through the store the verb will open.
	st, err := store.OpenBackend(context.Background(), dir, store.EngineSQLite, "", "", store.RW)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := orgknowledge.New(st).AddNote("leankg", "zz-note-verb probe", "local", "tester"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() { cmdNote([]string{"--project", dir, "--target", "leankg"}) })
	if !strings.Contains(out, "zz-note-verb probe") {
		t.Errorf("note list did not print the note it stored: %q", out)
	}
	// A target with no notes says so rather than printing nothing at all.
	out = captureStdout(t, func() { cmdNote([]string{"--project", dir, "--target", "no-such-target"}) })
	if !strings.Contains(out, "no-such-target") {
		t.Errorf("empty list is silent: %q", out)
	}
}
