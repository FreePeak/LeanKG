package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// This file is the READ-side bank surface the decision doc
// (xdev/docs/decisions/leankg-memory-backend.md, K3/K4) promised but the Go
// tree never had: stats, read-by-id, and list. Every route here is a read, so
// none of them belongs in auth.writePrefixes.
//
// Rows are read from the bank JSONL on every call — the same unindexed scan
// RecallBanks does. ponytail: O(rows) per call with no index and no freshness
// decay. Fine to ~10^4 rows/bank; past that, hoist this onto the same FTS/L3
// tier RecallBanks needs (decision doc K7), not onto a per-route cache.

// BankStats is the body of GET .../stats. The decision doc asked for entry
// count, banks, last-retain timestamp, memory root, and disk bytes; the wire
// shape stays flat so the client can print it without a schema.
type BankStats struct {
	Bank       string `json:"bank"`
	Entries    int    `json:"entries"`
	Bytes      int64  `json:"bytes"`
	Banks      int    `json:"banks"`       // banks on disk in this root
	LastRetain int64  `json:"last_retain"` // newest row's unix timestamp, 0 when empty
	Root       string `json:"root"`        // memory root (single-writer anchor)
}

// readBankRows parses one bank's JSONL into entries. A missing bank is empty,
// not an error: every read route answers an empty result for a bank that was
// never written, which is what makes the client's ensure-then-read walk work.
func (m *Memory) readBankRows(bank string) ([]Entry, error) {
	data, err := os.ReadFile(m.bankPath(bank))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("memory: read bank %s: %w", bank, err)
	}
	var rows []Entry
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var e Entry
		if json.Unmarshal([]byte(line), &e) != nil {
			continue // a torn line never fails the whole read
		}
		rows = append(rows, e)
	}
	return rows, nil
}

// Stats summarizes one bank. Entries counts every parsed row (including
// empty-content ones — this is a store report, not a recall).
func (m *Memory) Stats(bank string) (BankStats, error) {
	out := BankStats{Bank: bank, Root: m.root}
	rows, err := m.readBankRows(bank)
	if err != nil {
		return out, err
	}
	out.Entries = len(rows)
	for _, e := range rows {
		if e.Timestamp > out.LastRetain {
			out.LastRetain = e.Timestamp
		}
	}
	if fi, err := os.Stat(m.bankPath(bank)); err == nil {
		out.Bytes = fi.Size()
	}
	if files, err := os.ReadDir(filepath.Join(m.root, "banks")); err == nil {
		for _, f := range files {
			if !f.IsDir() && strings.HasSuffix(f.Name(), ".jsonl") {
				out.Banks++
			}
		}
	}
	return out, nil
}

// ByID returns the single row with this id. Both the wire id and the
// client-side document id are accepted (`RetainRaw` stores whatever the
// caller sent), because a harness reading back what it wrote does not always
// know which it holds.
func (m *Memory) ByID(bank, id string) (Entry, bool, error) {
	if strings.TrimSpace(id) == "" {
		return Entry{}, false, nil
	}
	rows, err := m.readBankRows(bank)
	if err != nil {
		return Entry{}, false, err
	}
	for _, e := range rows {
		if e.ID == id {
			return e, true, nil
		}
	}
	// Second pass: document_id in metadata. Kept separate so an exact id
	// always wins over a document id collision.
	for _, e := range rows {
		if doc, _ := e.Metadata["document_id"].(string); doc != "" && doc == id {
			return e, true, nil
		}
	}
	return Entry{}, false, nil
}

// List returns a bank's rows newest-first. offset/limit page the result
// (limit <= 0 = every remaining row); the newest-first default matches the
// order a client wants when it reads back what it just retained, and
// recentBanks' stable tie-break keeps the order reproducible.
func (m *Memory) List(bank string, offset, limit int) ([]Entry, int, error) {
	rows, err := m.readBankRows(bank)
	if err != nil {
		return nil, 0, err
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Timestamp > rows[j].Timestamp })
	total := len(rows)
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		return nil, total, nil
	}
	rows = rows[offset:]
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, total, nil
}

// Banks lists the bank names present in this root, sorted, without the .jsonl
// suffix. Read-only sibling of sessionCursor's directory walk.
func (m *Memory) Banks() ([]string, error) {
	files, err := os.ReadDir(filepath.Join(m.root, "banks"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("memory: list banks: %w", err)
	}
	var out []string
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
			continue
		}
		out = append(out, strings.TrimSuffix(f.Name(), ".jsonl"))
	}
	sort.Strings(out)
	return out, nil
}
