package memory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	m, err := Open(context.Background(), dir, false)
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
		{"create", func() error { return m.Create(context.Background(), "topics/probe.md", body) }},
		{"append", func() error { return m.Append(context.Background(), "topics/probe.md", body) }},
		{"insert", func() error {
			// insert/add READ the file first, so seed it the way production
			// callers do: these verbs edit an existing note.
			if err := m.Create(context.Background(), "topics/probe.md", "seed line\n"); err != nil {
				return err
			}
			return m.Insert(context.Background(), "topics/probe.md", body, 1)
		}},
		{"add", func() error {
			if err := m.Create(context.Background(), "topics/probe.md", "seed line\n"); err != nil {
				return err
			}
			return m.Add(context.Background(), "topics/probe.md", body)
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
	m, err := Open(context.Background(), dir, false)
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
	got, err := m.View(context.Background(), "topics/plain.md", 0)
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
	m, err := Open(context.Background(), dir, false)
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
	res, err := m.SessionRetain(context.Background(), ScopeGlobal, t.TempDir(), "redactprobe", "sess-1", turns, 2, nil)
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

// TestSessionRetainIndexesRedactedText pins CX-01: the FTS index is built from
// the same redacted rows the JSONL file receives. The bank write loop redacted
// a copy and then indexed the caller's unredacted slice, so a raw secret stayed
// searchable in memory_fts and recall returned it. Tags are covered too: they
// reach the index as JSON in the doc column.
//
// The secrets are invented shapes (FK-0001), never a real credential.
func TestSessionRetainIndexesRedactedText(t *testing.T) {
	m := openTest(t)
	const (
		apiKey    = "sk-abc123def456ghi789jkl"
		tagSecret = "hunter2plain"
	)
	turns := []string{"user: deploy with api_key=" + apiKey + " please"}
	res, err := m.SessionRetain(context.Background(), ScopeGlobal, t.TempDir(), "cx01probe", "sess-cx01", turns, 1, []string{"password=" + tagSecret})
	if err != nil {
		t.Fatal(err)
	}
	if res.Written == 0 {
		t.Fatal("retain wrote nothing; the probe is measuring nothing")
	}
	for _, raw := range []string{apiKey, tagSecret} {
		var n int
		if err := m.fts.QueryRow(`SELECT COUNT(*) FROM memory_fts WHERE instr(body, ?) > 0 OR instr(doc, ?) > 0`, raw, raw).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("memory_fts holds %d row(s) containing the raw secret %q", n, raw)
		}
	}
	var body string
	if err := m.fts.QueryRow(`SELECT body FROM memory_fts WHERE kind = 'bank'`).Scan(&body); err != nil {
		t.Fatalf("no bank row indexed: %v", err)
	}
	if !strings.Contains(body, "[REDACTED]") {
		t.Errorf("indexed body is not the redacted form: %q", body)
	}
}

// TestPreFixMemoryIsRedactedOnRead pins the upgrade path: memory written before
// the write-side redaction existed holds raw secrets on disk and in an index
// built at indexVersion "2". Opening the root must rebuild that index through
// the redactor, and every read path must return the redacted form, while the
// files on disk keep the raw bytes (a read never rewrites a file).
//
// The secrets are invented shapes (FK-0001), never a real credential.
func TestPreFixMemoryIsRedactedOnRead(t *testing.T) {
	dir := t.TempDir()
	m, err := Open(context.Background(), dir, false)
	if err != nil {
		t.Fatal(err)
	}
	const (
		apiKey = "sk-abc123def456ghi789jkl"
		jwt    = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N"
		bank   = "legacy"
	)
	raw := "rotate the key api_key=" + apiKey + " and retire Authorization: Bearer " + jwt
	files := map[string]string{
		"MEMORY.md":        "core: " + raw + "\n",
		"USER.md":          "user: " + raw + "\n",
		"topics/legacy.md": raw + "\n",
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(m.Root(), filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(m.Root(), "banks"), 0o755); err != nil {
		t.Fatal(err)
	}
	row, err := json.Marshal(Entry{ID: "legacy-1", Content: raw, Timestamp: time.Now().Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.bankPath(bank), append(row, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	// The index as the old version left it: raw file lines and raw bank rows,
	// marker "2". Reopening must see the stale marker and rebuild.
	for _, stmt := range []struct {
		q    string
		args []any
	}{
		{`INSERT INTO memory_fts(kind, path, bank, entry_id, doc, body) VALUES ('file', 'MEMORY.md', '', '', '', ?)`, []any{"core: " + raw}},
		{`INSERT INTO memory_fts(kind, path, bank, entry_id, doc, body) VALUES ('file', 'topics/legacy.md', '', '', '', ?)`, []any{raw}},
		{`INSERT INTO memory_fts(kind, path, bank, entry_id, doc, body) VALUES ('bank', '', ?, ?, ?, ?)`, []any{bank, "legacy-1", string(row), raw}},
		{`INSERT OR REPLACE INTO memory_meta (key, value) VALUES ('index_version', '2')`, nil},
	} {
		if _, err := m.fts.Exec(stmt.q, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = Open(context.Background(), dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	assertClean := func(t *testing.T, what, got string) {
		t.Helper()
		if strings.Contains(got, apiKey) || strings.Contains(got, jwt) {
			t.Errorf("%s returned the raw secret: %q", what, got)
		}
		if !strings.Contains(got, "[REDACTED") {
			t.Errorf("%s has no redaction marker: %q", what, got)
		}
	}

	t.Run("snapshot", func(t *testing.T) {
		got, err := m.Snapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		assertClean(t, "Snapshot", got)
	})
	t.Run("view", func(t *testing.T) {
		got, err := m.View(context.Background(), "topics/legacy.md", 0)
		if err != nil {
			t.Fatal(err)
		}
		assertClean(t, "View", got)
	})
	t.Run("search", func(t *testing.T) {
		hits, err := m.Search(context.Background(), "rotate", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) == 0 {
			t.Fatal("Search found nothing; the probe is measuring nothing")
		}
		for _, h := range hits {
			assertClean(t, "Search snippet "+h.Path, h.Snippet)
		}
	})
	t.Run("recall indexed", func(t *testing.T) {
		rows, err := m.RecallBanks(context.Background(), []string{bank}, "rotate key", 8)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			t.Fatal("RecallBanks found nothing; the probe is measuring nothing")
		}
		for _, e := range rows {
			assertClean(t, "RecallBanks content", e.Content)
		}
	})
	t.Run("recent", func(t *testing.T) {
		text, entries, err := m.FirstTurnMemories(context.Background(), ScopeGlobal, "", bank, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) == 0 {
			t.Fatal("cold recall found nothing; the probe is measuring nothing")
		}
		assertClean(t, "FirstTurnMemories text", text)
		for _, e := range entries {
			assertClean(t, "FirstTurnMemories entry", e.Content)
		}
	})
	t.Run("by id", func(t *testing.T) {
		e, ok, err := m.ByID(bank, "legacy-1")
		if err != nil || !ok {
			t.Fatalf("ByID: ok=%v err=%v", ok, err)
		}
		assertClean(t, "ByID", e.Content)
	})
	t.Run("list", func(t *testing.T) {
		rows, _, err := m.List(bank, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range rows {
			assertClean(t, "List", e.Content)
		}
	})

	t.Run("index rebuilt", func(t *testing.T) {
		if indexVersion == "2" {
			t.Error("indexVersion is still \"2\": existing indexes are never rebuilt")
		}
		var v string
		if err := m.fts.QueryRow(`SELECT value FROM memory_meta WHERE key = 'index_version'`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		if v != indexVersion {
			t.Errorf("index_version = %q, want %q: the stale index was not rebuilt", v, indexVersion)
		}
		for _, s := range []string{apiKey, jwt} {
			var n int
			if err := m.fts.QueryRow(`SELECT COUNT(*) FROM memory_fts WHERE instr(body, ?) > 0 OR instr(doc, ?) > 0`, s, s).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Errorf("index holds %d row(s) with the raw secret %q", n, s)
			}
		}
	})

	t.Run("disk untouched", func(t *testing.T) {
		paths := map[string]string{
			"MEMORY.md":          filepath.Join(m.Root(), "MEMORY.md"),
			"USER.md":            filepath.Join(m.Root(), "USER.md"),
			"topics/legacy.md":   filepath.Join(m.Root(), "topics", "legacy.md"),
			"banks/legacy.jsonl": m.bankPath(bank),
		}
		for rel, full := range paths {
			data, err := os.ReadFile(full)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), apiKey) || !strings.Contains(string(data), jwt) {
				t.Errorf("%s was rewritten by a read: %q", rel, data)
			}
		}
	})
}

// TestRedactIsIdempotent pins that redacting already-redacted text is a no-op,
// so a read can redact stored text without changing a row that the write path
// already masked, and a rebuilt index matches the write-time index.
func TestRedactIsIdempotent(t *testing.T) {
	for _, s := range []string{
		"rotate api_key=sk-abc123def456ghi789jkl and Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N",
		`{"content":"password=hunter2plain","metadata":{"tags":["token=abcdefgh1234"]}}`,
		"-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBAKj34GkxFhD90vcNYYcNqEx\n-----END RSA PRIVATE KEY-----",
		"plain text with nothing to mask",
	} {
		once := Redact(s)
		if twice := Redact(once); twice != once {
			t.Errorf("Redact is not idempotent:\n once:  %q\n twice: %q", once, twice)
		}
	}
}
