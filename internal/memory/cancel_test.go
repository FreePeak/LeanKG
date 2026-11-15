package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cancelledCtx returns a context that is already cancelled.
func cancelledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// seedDocRow writes one row carrying document_id doc-1 to bank, so a delete
// or replace on that bank has something it would remove if it were allowed.
func seedDocRow(t *testing.T, m *Memory, bank string) {
	t.Helper()
	if err := m.RetainRaw(context.Background(), bank, []Entry{{
		Content:  "seed row",
		Metadata: map[string]any{"document_id": "doc-1"},
	}}); err != nil {
		t.Fatal(err)
	}
}

// bankRowCounts reports the JSONL line count of a bank file and the rows the
// FTS index holds for it. A write must keep the two in step, so a cancelled
// write must leave both unchanged.
func bankRowCounts(t *testing.T, m *Memory, bank string) (lines, indexed int) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(m.Root(), "banks", bank+".jsonl"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, l := range strings.Split(string(data), "\n") {
		if l != "" {
			lines++
		}
	}
	if err := m.fts.QueryRow(`SELECT COUNT(*) FROM memory_fts WHERE kind = 'bank' AND bank = ?`, sanitizeBank(bank)).Scan(&indexed); err != nil {
		t.Fatal(err)
	}
	return lines, indexed
}

// TestWritesHonourCancelledContext pins the CX-34 rule that a cancelled
// context stops a write before any side effect: the call returns ctx.Err(),
// and neither the bank file nor memory_fts changes.
func TestWritesHonourCancelledContext(t *testing.T) {
	writes := map[string]func(ctx context.Context, m *Memory, bank string) error{
		"Retain": func(ctx context.Context, m *Memory, bank string) error {
			_, err := m.Retain(ctx, bank, []Entry{{Content: "beta row"}}, 5)
			return err
		},
		"RetainRaw": func(ctx context.Context, m *Memory, bank string) error {
			return m.RetainRaw(ctx, bank, []Entry{{Content: "beta row"}})
		},
		"RetainReplacing": func(ctx context.Context, m *Memory, bank string) error {
			_, err := m.RetainReplacing(ctx, bank, []Entry{{Content: "beta row"}}, []string{"doc-1"})
			return err
		},
		"SessionRetain": func(ctx context.Context, m *Memory, bank string) error {
			_, err := m.SessionRetain(ctx, ScopeGlobal, "", bank, "sess-cancel", []string{"turn one"}, 1, nil)
			return err
		},
		"DeleteDocument": func(ctx context.Context, m *Memory, bank string) error {
			_, err := m.DeleteDocument(ctx, bank, "doc-1")
			return err
		},
	}
	for name, write := range writes {
		t.Run(name, func(t *testing.T) {
			m := openTest(t)
			bank := "cx34-" + strings.ToLower(name)
			seedDocRow(t, m, bank)
			lines0, indexed0 := bankRowCounts(t, m, bank)

			err := write(cancelledCtx(), m, bank)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled %s: err = %v, want context.Canceled", name, err)
			}
			lines1, indexed1 := bankRowCounts(t, m, bank)
			if lines1 != lines0 || indexed1 != indexed0 {
				t.Errorf("cancelled %s wrote: file rows %d -> %d, indexed rows %d -> %d",
					name, lines0, lines1, indexed0, indexed1)
			}
		})
	}
}

// TestRecallHonoursCancelledContext pins the CX-34 rule that a cancelled
// context makes every recall return ctx.Err() instead of rows.
func TestRecallHonoursCancelledContext(t *testing.T) {
	m := openTest(t)
	const bank = "cx34-recall"
	if err := m.RetainRaw(context.Background(), bank, []Entry{{Content: "alpha beta gamma"}}); err != nil {
		t.Fatal(err)
	}
	reads := map[string]func(ctx context.Context) error{
		"Recall": func(ctx context.Context) error {
			_, err := m.Recall(ctx, bank, "alpha", 5)
			return err
		},
		"RecallBanks": func(ctx context.Context) error {
			_, err := m.RecallBanks(ctx, []string{bank}, "alpha", 5)
			return err
		},
		"RecallFiltered": func(ctx context.Context) error {
			_, err := m.RecallFiltered(ctx, []string{bank}, "alpha", 5, nil)
			return err
		},
		"SessionRecall": func(ctx context.Context) error {
			_, _, err := m.SessionRecall(ctx, ScopeGlobal, "", bank, "alpha", 5)
			return err
		},
		"FirstTurnMemories": func(ctx context.Context) error {
			_, _, err := m.FirstTurnMemories(ctx, ScopeGlobal, "", bank, "alpha")
			return err
		},
		// The cold path (empty query) reads the bank files directly, with no
		// SQL that could honour ctx, so only the explicit check stops it.
		"FirstTurnMemories cold": func(ctx context.Context) error {
			_, _, err := m.FirstTurnMemories(ctx, ScopeGlobal, "", bank, "")
			return err
		},
	}
	for name, read := range reads {
		if err := read(cancelledCtx()); !errors.Is(err, context.Canceled) {
			t.Errorf("cancelled %s: err = %v, want context.Canceled", name, err)
		}
	}
	// Control: the same reads succeed on a live context, so the probe above
	// is measuring cancellation and not an empty bank.
	entries, err := m.Recall(context.Background(), bank, "alpha", 5)
	if err != nil || len(entries) == 0 {
		t.Fatalf("control recall on a live context: %d rows, err %v", len(entries), err)
	}
}
