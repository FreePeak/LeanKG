package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMemoryWriteRedactsSecrets is FR-SMA-06: a secret in a lesson, a diary
// entry or an appended line must not land on disk verbatim.
//
// Measured live on the self-host before this test: `import action=memory
// command=create path=topics/secret-probe.md content=<fake keys>` wrote the
// file and grep found BOTH `sk-abc123...` and the `eyJhbGci...` JWT intact.
// Two redactors already existed in the tree — telemetry.Redact and
// sessionlink.Clip — and neither is on this path, so the same lesson an
// operator writes into a memory file is the one a telemetry row would have
// masked.
//
// The secrets here are invented shapes (FK-0001), never a real credential.
func TestMemoryWriteRedactsSecrets(t *testing.T) {
	dir := t.TempDir()
	m, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	const (
		apiKey = "sk-abc123def456ghi789jkl"
		jwt    = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N"
	)
	body := "lesson: rotate the key api_key=" + apiKey + " before shipping, " +
		"and retire the old Authorization: Bearer " + jwt + " token"
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"create", func() error { return m.Create("topics/probe.md", body) }},
		{"append", func() error { return m.Append("topics/probe.md", body) }},
		{"insert", func() error {
			// insert/add READ the file first, so seed it the way production
			// callers do: these verbs edit an existing note.
			if err := m.Create("topics/probe.md", "seed line\n"); err != nil {
				return err
			}
			return m.Insert("topics/probe.md", body, 1)
		}},
		{"add", func() error {
			if err := m.Create("topics/probe.md", "seed line\n"); err != nil {
				return err
			}
			return m.Add("topics/probe.md", body)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.MkdirAll(filepath.Join(dir, "topics"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(m.Root(), "topics"), 0o755); err != nil {
				t.Fatal(err)
			}
			full := filepath.Join(m.Root(), "topics", "probe.md")
			_ = os.Remove(full)
			if err := tc.call(); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(full)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), apiKey) {
				t.Errorf("%s stored the api key verbatim: %q", tc.name, data)
			}
			if strings.Contains(string(data), jwt) {
				t.Errorf("%s stored the JWT verbatim: %q", tc.name, data)
			}
			if !strings.Contains(string(data), "[REDACTED]") {
				t.Errorf("%s redacted nothing — the caller's text is gone or the marker moved: %q", tc.name, data)
			}
		})
	}
}

// TestMemoryReadsUntouched pins that a READ never rewrites a file: redaction
// happens at the write boundary, so an existing file keeps exactly the bytes
// the operator put there.
func TestMemoryReadsUntouched(t *testing.T) {
	dir := t.TempDir()
	m, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	const raw = "verbatim operator text, no secrets here\n"
	if err := os.MkdirAll(filepath.Join(m.Root(), "topics"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.Root(), "topics", "plain.md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := m.View("topics/plain.md", 0)
	if err != nil {
		t.Fatal(err)
	}
	// View trims the trailing newline by contract; the FILE must keep it.
	if got != strings.TrimSuffix(raw, "\n") {
		t.Fatalf("read changed the content: got %q want %q", got, raw)
	}
	onDisk, err := os.ReadFile(filepath.Join(m.Root(), "topics", "plain.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != raw {
		t.Fatalf("read rewrote the file on disk: got %q want %q", onDisk, raw)
	}
}

// TestSessionRetainRedactsSecrets pins the second write path: a retained
// transcript turn is the highest-volume writer in the whole system (every
// user/assistant turn of every session), so a secret pasted once into a
// prompt is otherwise in the bank verbatim forever.
func TestSessionRetainRedactsSecrets(t *testing.T) {
	dir := t.TempDir()
	m, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	const (
		apiKey = "sk-abc123def456ghi789jkl"
		jwt    = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N"
	)
	turns := []string{
		"user: deploy with api_key=" + apiKey + " please",
		"assistant: the old Authorization: Bearer " + jwt + " is rotated now",
	}
	res, err := m.SessionRetainCtx(context.Background(), ScopeGlobal, t.TempDir(), "redactprobe", "sess-1", turns, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Written == 0 {
		t.Fatal("retain wrote nothing; the probe is measuring nothing")
	}
	data, err := os.ReadFile(filepath.Join(m.Root(), "banks", "redactprobe.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), apiKey) {
		t.Errorf("retained bank row kept the api key: %s", data)
	}
	if strings.Contains(string(data), jwt) {
		t.Errorf("retained bank row kept the JWT: %s", data)
	}
}
