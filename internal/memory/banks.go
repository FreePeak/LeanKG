package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/bits"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Bank adapter: mnemopi-compatible JSONL transcript banks under
// root/banks/<bank>.jsonl. The JSONL row shape mirrors the Rust
// MemoryEntry (src/memory/store.rs) so banks written by either engine
// read identically.

// Entry is one retained memory row.
type Entry struct {
	ID         string         `json:"id"`
	Content    string         `json:"content"`
	Source     string         `json:"source"`
	Timestamp  int64          `json:"timestamp"` // unix seconds
	Importance float64        `json:"importance"`
	Cwd        string         `json:"cwd"`
	Metadata   map[string]any `json:"metadata"`

	// Score is the relative relevance of this row in the recall that
	// returned it (higher is better). Recall sets it on the rows it returns:
	// -bm25 for a lexical-only ranking, the reciprocal-rank-fusion sum when
	// the dense arm contributed. Values compare only within one response.
	// It is never persisted.
	Score float64 `json:"-"`
}

// bankPath sanitizes the bank name and appends .jsonl, matching the Rust
// MemoryStore::bank_path transform.
func (m *Memory) bankPath(bank string) string {
	return filepath.Join(m.root, "banks", sanitizeBank(bank)+".jsonl")
}

// BankName derives the mnemopi bank name for a working directory:
// sanitize(basename(cwd)) + "-" + base36(wyhash64(canoncialized abs cwd,
// seed 0)), sanitized and capped at 64 chars. Derived from the CWD ONLY,
// never the git root (upstream bug #2412).
//
// wyhash64 below is a byte-exact port of the Rust `wyhash` crate 0.5
// (wyhash "final" v3), the same algorithm LeanKG's Rust line uses. NOTE:
// cross-runtime parity with OMP's Bun.hash is UNVERIFIED — the same caveat
// exists on the Rust side; banks fragment if the hash ever diverges.
func BankName(cwd string) string {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		abs = cwd
	}
	// Canonicalize like Rust fs::canonicalize: resolve symlinks when the
	// path exists, so symlinked routes hash identically.
	if resolved, rerr := filepath.EvalSymlinks(abs); rerr == nil {
		abs = resolved
	}
	base := filepath.Base(abs)
	if base == "/" || base == "." {
		base = abs
	}
	h := base36(wyhash64([]byte(abs), 0))
	return sanitizeBank(base + "-" + h)
}

// sanitizeBank maps bank names to <=64 chars of [A-Za-z0-9_-]
// (mnemopi/config.ts:176-186 semantics, mirrored from bank.rs).
func sanitizeBank(name string) string {
	var b strings.Builder
	for _, c := range name {
		if unicode.IsLetter(c) && c < 128 || unicode.IsDigit(c) && c < 128 || c == '_' || c == '-' {
			b.WriteRune(c)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if len(out) > 64 {
		out = out[:64] // ASCII-only by construction
	}
	if out == "" {
		out = "_"
	}
	return out
}

// base36 renders v in lowercase base-36, mirroring bank.rs.
func base36(v uint64) string {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	if v == 0 {
		return "0"
	}
	var out []byte
	for v > 0 {
		out = append(out, alphabet[v%36])
		v /= 36
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

// wyhash64 hashes b with seed using the wyhash "final" v3 algorithm,
// byte-for-byte the Rust wyhash 0.5 crate (functions.rs). Vectors pinned in
// tests come from running that crate.
func wyhash64(b []byte, seed uint64) uint64 {
	const (
		p0 = 0xa0761d6478bd642f
		p1 = 0xe7037ed1a0b428db
		p2 = 0x8ebc6af09c88c6e3
		p3 = 0x589965cc75374cc3
		p4 = 0x1d8e4e27c47d124f
		p5 = 0xeb44accab455d165
	)
	mum := func(a, b uint64) uint64 {
		hi, lo := bits.Mul64(a, b)
		return hi ^ lo
	}
	read32 := func(b []byte) uint64 {
		return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24
	}
	read64 := func(b []byte) uint64 {
		return read32(b) | read32(b[4:])<<32
	}
	// The crate's read64_swapped: 32-bit halves swapped relative to read64.
	read64Swapped := func(b []byte) uint64 {
		return read32(b)<<32 | read32(b[4:])
	}
	// read_rest covers 1..=8 bytes with the crate's (quirky) byte order.
	readRest := func(b []byte) uint64 {
		switch len(b) {
		case 1:
			return uint64(b[0])
		case 2:
			return uint64(b[1])<<8 | uint64(b[0])
		case 3:
			return uint64(b[1])<<16 | uint64(b[0])<<8 | uint64(b[2])
		case 4:
			return read32(b)
		case 5:
			return read32(b)<<8 | uint64(b[4])
		case 6:
			return read32(b)<<16 | uint64(b[5])<<8 | uint64(b[4])
		case 7:
			return read32(b)<<24 | uint64(b[5])<<16 | uint64(b[4])<<8 | uint64(b[6])
		default:
			return read64Swapped(b)
		}
	}

	n := len(b)
	for len(b) >= 32 {
		seed = mum(seed^p0,
			mum(read64(b)^p1, read64(b[8:])^p2)^
				mum(read64(b[16:])^p3, read64(b[24:])^p4))
		b = b[32:]
	}
	seed ^= p0
	if rest := len(b); rest != 0 { // remaining bytes after 32-byte chunks
		tail := b
		switch ((n - 1) & 31) / 8 {
		case 0:
			seed = mum(seed, readRest(tail)^p1)
		case 1:
			seed = mum(read64Swapped(tail)^seed, readRest(tail[8:])^p2)
		case 2:
			seed = mum(read64Swapped(tail)^seed, read64Swapped(tail[8:])^p2) ^
				mum(seed, readRest(tail[16:])^p3)
		default: // 3
			seed = mum(read64Swapped(tail)^seed, read64Swapped(tail[8:])^p2) ^
				mum(read64Swapped(tail[16:])^seed, readRest(tail[24:])^p4)
		}
	}
	return mum(seed, uint64(n)^p5)
}

// TranscriptSource and TranscriptImportance mirror the Rust retain
// contract (store.rs:16-17): harness transcript rows carry a fixed source
// tag and importance.
const (
	TranscriptSource     = "coding-agent-transcript"
	TranscriptImportance = 0.65
)

// RetainResult reports one transcript batch (Rust RetainResult,
// store.rs:291-296).
type RetainResult struct {
	Bank                    string `json:"bank"`
	Written                 int    `json:"written"`
	Skipped                 int    `json:"skipped"`
	RetainedThroughUserTurn int    `json:"retained_through_user_turn"`
}

// SessionRetain ports the reference session_retain tool
// (mcp/handler.rs:2989-3019): frame transcript turns into mnemopi rows and
// append them to the bank the (scope, cwd, bank) triple selects. Resolution
// order: a non-empty bank names one bank exactly (the REST /banks/{bank}
// mode); otherwise scope.WriteBank maps the cwd's mnemopi bank (bank.rs
// write_bank; Global targets the shared bank instead). An empty cwd falls
// back to this store's own project dir. The batch is idempotent per session
// (store.rs:92-108): a retained_through_user_turn at or below the session's
// stored cursor skips the whole batch.
func (m *Memory) SessionRetain(scope Scope, cwd, bank, sessionID string, turns []string, throughUserTurn int, tags []string) (RetainResult, error) {
	return m.SessionRetainCtx(context.Background(), scope, cwd, bank, sessionID, turns, throughUserTurn, tags)
}

// SessionRetainCtx is SessionRetain with the caller's context, so the retain
// event joins the transport call that made it (telemetry.WithIdentity).
func (m *Memory) SessionRetainCtx(ctx context.Context, scope Scope, cwd, bank, sessionID string, turns []string, throughUserTurn int, tags []string) (RetainResult, error) {
	start := time.Now()
	if sessionID == "" {
		return RetainResult{}, fmt.Errorf("memory: session_retain requires session_id")
	}
	if len(turns) == 0 {
		return RetainResult{}, fmt.Errorf("memory: session_retain requires turns (array of transcript turn texts)")
	}
	if throughUserTurn < 0 {
		return RetainResult{}, fmt.Errorf("memory: retained_through_user_turn must be >= 0")
	}
	target := m.WriteBank(scope, cwd, bank)
	if seen, ok := m.sessionCursor(sessionID); ok && throughUserTurn <= seen {
		recordRetain(ctx, target, 0, len(turns), 0, start)
		return RetainResult{Bank: target, Skipped: len(turns), RetainedThroughUserTurn: seen}, nil
	}
	ms := time.Now().UnixMilli()
	sourceID := fmt.Sprintf("%s-%d", sessionID, ms)
	entries := make([]Entry, 0, len(turns))
	for i, turn := range turns {
		// Trust boundary: an empty row is permanently unrecallable
		// (zero-match filtering) — reject rather than swallow (same
		// contract the REST retain route enforces).
		if strings.TrimSpace(turn) == "" {
			return RetainResult{}, fmt.Errorf("memory: turns[%d] is empty — empty entries would be unrecallable", i)
		}
		entries = append(entries, Entry{
			ID:         fmt.Sprintf("%s-%d", sourceID, i),
			Content:    turn,
			Source:     TranscriptSource,
			Timestamp:  ms / 1000,
			Importance: TranscriptImportance,
			Cwd:        cwd,
			Metadata:   entryMetadata(sessionID, sourceID, len(turns), throughUserTurn, cwd, tags),
		})
	}
	if err := m.appendBank(target, entries); err != nil {
		return RetainResult{}, err
	}
	recordRetain(ctx, target, len(entries), 0, 0, start)
	return RetainResult{Bank: target, Written: len(entries), RetainedThroughUserTurn: throughUserTurn}, nil
}

// SessionRecall ports the reference session_recall tool
// (mcp/handler.rs:3023-3043): merged ranked recall across the read banks
// the (scope, cwd, bank) triple selects. Resolution order matches
// SessionRetain: a non-empty bank reads that bank only; otherwise
// scope.ReadBanks merges in matrix order (project first, shared appended
// in tagged mode; bank.rs:88-95). Returns the consulted banks plus the
// ranked rows; limit <= 0 means 8 (the OMP recallLimit).
func (m *Memory) SessionRecall(scope Scope, cwd, bank, query string, limit int) (banks []string, entries []Entry, err error) {
	return m.SessionRecallCtx(context.Background(), scope, cwd, bank, query, limit)
}

// SessionRecallCtx is SessionRecall with the caller's context (see
// SessionRetainCtx).
func (m *Memory) SessionRecallCtx(ctx context.Context, scope Scope, cwd, bank, query string, limit int) (banks []string, entries []Entry, err error) {
	banks = m.sessionReadBanks(scope, cwd, bank)
	entries, err = m.RecallBanksCtx(ctx, banks, query, limit)
	return banks, entries, err
}

// WriteBank resolves the bank a write targets for the (scope, cwd, bank)
// triple: a non-empty bank names one bank exactly (the REST /banks/{bank}
// mode); otherwise scope.WriteBank maps the cwd's mnemopi bank (bank.rs
// write_bank; Global targets the shared bank instead). An empty cwd falls
// back to this store's own project dir.
//
// It is the ONE resolution path: SessionRetain and the bank-row delete both
// go through it, so a row is always written to and deleted from the same
// bank.
func (m *Memory) WriteBank(scope Scope, cwd, bank string) string {
	if bank != "" {
		return bank
	}
	return scope.WriteBank(BankName(m.scopeCwd(cwd)))
}

// sessionReadBanks resolves the read-bank list for the (scope, cwd, bank)
// triple: a non-empty bank names one bank exactly (bank-name mode — the
// REST /banks/{bank} contract); otherwise the scope matrix maps the cwd's
// mnemopi bank.
func (m *Memory) sessionReadBanks(scope Scope, cwd, bank string) []string {
	if bank != "" {
		return []string{bank}
	}
	return scope.ReadBanks(BankName(m.scopeCwd(cwd)))
}

// scopeCwd applies the reference's cwd default (handler.rs:3004-3008: an
// absent cwd falls back to the process dir; on this side the store's own
// project dir is the equivalent anchor).
func (m *Memory) scopeCwd(cwd string) string {
	if cwd != "" {
		return cwd
	}
	return filepath.Dir(filepath.Dir(m.root))
}

// sessionCursor reports the highest retained_through_user_turn stored for a
// session across every bank (Rust retained_through_user_turn, store.rs:64-87
// — session-keyed, so concurrent sessions sharing one bank never suppress
// each other's batches; the REST Retain contract stays per-bank by design).
func (m *Memory) sessionCursor(sessionID string) (int, bool) {
	return m.cursor("session", sessionID)
}

// Retain appends a transcript batch to a bank as JSONL, tagging every row's
// metadata with the retained_through_user_turn cursor. Idempotent per bank:
// when throughUserTurn is at or below the bank's stored cursor the write is
// skipped entirely (resume-safety; the REST/mnemopi contract keys it per
// bank — SessionRetain below keys the same check per session, matching the
// reference handler).
//
// The result says what happened: a skipped batch reports Skipped and the
// stored cursor, never a write (RS-07: the REST handler answered
// `retained: N` for batches this gate had dropped).
func (m *Memory) Retain(bank string, entries []Entry, throughUserTurn int) (RetainResult, error) {
	return m.RetainCtx(context.Background(), bank, entries, throughUserTurn)
}

// RetainCtx is Retain with the caller's context (see SessionRetainCtx).
func (m *Memory) RetainCtx(ctx context.Context, bank string, entries []Entry, throughUserTurn int) (RetainResult, error) {
	start := time.Now()
	if cur := m.bankCursor(bank); throughUserTurn <= cur {
		recordRetain(ctx, bank, 0, len(entries), 0, start)
		return RetainResult{Bank: bank, Skipped: len(entries), RetainedThroughUserTurn: cur}, nil
	}
	for i := range entries {
		if entries[i].Metadata == nil {
			entries[i].Metadata = map[string]any{}
		}
		entries[i].Metadata["retained_through_user_turn"] = throughUserTurn
	}
	applyEntryDefaults(entries)
	if err := m.appendBank(bank, entries); err != nil {
		return RetainResult{}, err
	}
	recordRetain(ctx, bank, len(entries), 0, 0, start)
	return RetainResult{Bank: bank, Written: len(entries), RetainedThroughUserTurn: throughUserTurn}, nil
}

// RetainRaw appends entries WITHOUT the user-turn cursor gate: same
// id/source/timestamp/importance defaults as Retain, but every call writes.
// The hindsight-compat mount (#414) uses this — the Hindsight retain wire has
// no retained_through_user_turn concept, and routing it through Retain's
// cursor would silently drop every write after the first (a missing cursor
// is 0, and 0 <= 0 short-circuits).
func (m *Memory) RetainRaw(bank string, entries []Entry) error {
	return m.RetainRawCtx(context.Background(), bank, entries)
}

// RetainRawCtx is RetainRaw with the caller's context (see SessionRetainCtx).
func (m *Memory) RetainRawCtx(ctx context.Context, bank string, entries []Entry) error {
	start := time.Now()
	applyEntryDefaults(entries)
	if err := m.appendBank(bank, entries); err != nil {
		return err
	}
	recordRetain(ctx, bank, len(entries), 0, 0, start)
	return nil
}

// applyEntryDefaults fills ID/Source/Timestamp/Importance exactly as the
// session-retained path does (Retain's pre-append loop).
func applyEntryDefaults(entries []Entry) {
	for i := range entries {
		if entries[i].Metadata == nil {
			entries[i].Metadata = map[string]any{}
		}
		if entries[i].ID == "" {
			// Batch-indexed: bare UnixNano collides within a tight loop
			// (coarse clock) and merged recall dedupes by id.
			entries[i].ID = fmt.Sprintf("%d-%d", time.Now().UnixNano(), i)
		}
		if entries[i].Source == "" {
			entries[i].Source = TranscriptSource
		}
		if entries[i].Timestamp == 0 {
			entries[i].Timestamp = time.Now().Unix()
		}
		if entries[i].Importance == 0 {
			entries[i].Importance = TranscriptImportance
		}
	}
}

// RetainReplacing is RetainRaw with Hindsight document upsert semantics:
// every existing row whose metadata document_id is in replaceDocs is dropped
// before entries are appended, under one lock so a concurrent retain never
// lands between the drop and the append. An empty replaceDocs is RetainRaw.
func (m *Memory) RetainReplacing(bank string, entries []Entry, replaceDocs []string) error {
	_, err := m.RetainReplacingCtx(context.Background(), bank, entries, replaceDocs)
	return err
}

// RetainReplacingCtx is RetainReplacing that also reports how many existing
// rows the document upsert dropped (the replaced count). The count goes to
// telemetry; the Hindsight retain response does not carry it.
func (m *Memory) RetainReplacingCtx(ctx context.Context, bank string, entries []Entry, replaceDocs []string) (int, error) {
	start := time.Now()
	applyEntryDefaults(entries)
	m.bankMu.Lock()
	defer m.bankMu.Unlock()
	replaced := 0
	if len(replaceDocs) > 0 {
		drop := make(map[string]bool, len(replaceDocs))
		for _, d := range replaceDocs {
			drop[d] = true
		}
		n, err := m.rewriteBankLocked(bank, func(e Entry) bool { return drop[entryDocumentID(e)] })
		if err != nil {
			return 0, err
		}
		replaced = n
	}
	if err := m.appendBankLocked(bank, entries); err != nil {
		return replaced, err
	}
	recordRetain(ctx, bank, len(entries), 0, replaced, start)
	return replaced, nil
}

// DeleteDocument removes every row of one document from a bank and reports
// how many rows went. A missing bank or unknown document deletes nothing.
func (m *Memory) DeleteDocument(bank, documentID string) (int, error) {
	return m.DeleteDocumentCtx(context.Background(), bank, documentID)
}

// DeleteDocumentCtx is DeleteDocument with the caller's context (see
// SessionRetainCtx).
func (m *Memory) DeleteDocumentCtx(ctx context.Context, bank, documentID string) (int, error) {
	if strings.TrimSpace(documentID) == "" {
		return 0, nil
	}
	start := time.Now()
	m.bankMu.Lock()
	defer m.bankMu.Unlock()
	n, err := m.rewriteBankLocked(bank, func(e Entry) bool { return entryDocumentID(e) == documentID })
	if err != nil {
		return 0, err
	}
	recordDelete(ctx, bank, n, start)
	return n, nil
}

// entryDocumentID reads the client document id RetainRaw stored in metadata.
func entryDocumentID(e Entry) string {
	doc, _ := e.Metadata["document_id"].(string)
	return doc
}

// contentDocumentID is the stable content identity of a bank row: a hash of
// the normalized body.
//
// It exists because the row id cannot be the identity. applyEntryDefaults
// mints ids per WRITE ("<unixnano>-<index>"), so two rows of the same body
// never share an id and a dedupe keyed on the id can never fire — measured
// on the live leankg memory bank: 448 rows, 304 distinct bodies, the worst
// group 67 copies of one 46-byte body, and a recall for it returned the same
// sentence 72 times. This hash is the one identity that IS the same for the
// same body, so the rewrite path (which selects rows by document id) can
// collapse them.
func contentDocumentID(content string) string {
	sum := sha256.Sum256([]byte(normalizeBody(content)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// normalizeBody folds a body to its identity form: runs of whitespace
// collapsed to one space, ends trimmed. Two bodies that differ only in
// spacing are the same memory.
func normalizeBody(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// rowDocID is the row's document identity: the client's metadata document_id
// when it has one, else the content fallback. This is the "document_id
// fallback" — a row the client wrote without a document id still has one
// identity, and it is derived from the body, so it is stable across writes.
func rowDocID(e Entry) string {
	if d := entryDocumentID(e); d != "" {
		return d
	}
	return contentDocumentID(e.Content)
}

// rowKey is a bank row's identity for the dedupe: the document identity
// together with the hash of the body.
//
// The body is part of the key on purpose: one client document may span
// several rows (a chunked upload keeps one document_id and one row per
// chunk), and those rows share a document id but differ in body — they are
// NOT duplicates. Rows that share BOTH the identity and the body are the
// same memory written twice, and only those collapse.
func rowKey(e Entry) string {
	return rowDocID(e) + "\x00" + contentDocumentID(e.Content)
}

// rewriteBankLocked drops the rows drop selects and atomically replaces the
// bank file (temp file + rename, so a crash leaves the old bank intact).
// Lines that fail to parse are kept verbatim: a rewrite must never be the
// thing that loses data. Callers hold bankMu.
func (m *Memory) rewriteBankLocked(bank string, drop func(Entry) bool) (int, error) {
	path := m.bankPath(bank)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("memory: read bank %s: %w", bank, err)
	}
	var kept strings.Builder
	removed := 0
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var e Entry
		if json.Unmarshal([]byte(line), &e) == nil && drop(e) {
			removed++
			continue
		}
		kept.WriteString(line)
		kept.WriteByte('\n')
	}
	if removed == 0 {
		return 0, nil
	}
	if err := writeBankFile(path, kept.String()); err != nil {
		return 0, err
	}
	if err := m.reindexBankLocked(bank); err != nil {
		return removed, err
	}
	return removed, nil
}

// writeBankFile replaces a bank file atomically: the new content goes to a
// temp file next to it and a rename puts it in place. A crash leaves the
// old bank intact — a compaction must never be the thing that loses data.
func writeBankFile(path, content string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return fmt.Errorf("memory: rewrite bank %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("memory: rewrite bank %s: %w", path, err)
	}
	return nil
}

// compactBanks rewrites every bank file under the memory root with its
// duplicate rows collapsed. Callers hold no lock: each bank is compacted
// under bankMu.
func (m *Memory) compactBanks() error {
	banks, err := os.ReadDir(filepath.Join(m.root, "banks"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("memory: read banks dir: %w", err)
	}
	for _, b := range banks {
		if b.IsDir() || !strings.HasSuffix(b.Name(), ".jsonl") {
			continue
		}
		bank := strings.TrimSuffix(b.Name(), ".jsonl")
		m.bankMu.Lock()
		_, err := m.compactBankLocked(bank)
		m.bankMu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

// compactBankLocked rewrites one bank file with its duplicate rows
// collapsed: one row per identity (the row's document identity together
// with its body), keeping the LAST copy of each group — the newest one, and
// the row a write-time dedupe would have left behind.
//
// The JSONL file is the source of truth on disk, so a compaction that
// touched only the FTS side left the duplicates in the file forever
// (measured: the live bank grew ~88.6 KB/day, ~45 rows/day). The write is
// crash-safe (temp file + rename) and the index is re-derived from the
// rewritten file. Callers hold bankMu.
func (m *Memory) compactBankLocked(bank string) (int, error) {
	path := m.bankPath(bank)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("memory: read bank %s: %w", bank, err)
	}
	lines := make([]string, 0, 64)
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	last := make(map[string]int, len(lines))
	for i, line := range lines {
		var e Entry
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		last[rowKey(e)] = i
	}
	var kept strings.Builder
	removed := 0
	for i, line := range lines {
		var e Entry
		if json.Unmarshal([]byte(line), &e) != nil {
			// A line that does not parse is kept verbatim: a rewrite must
			// never be the thing that loses data.
			kept.WriteString(line)
			kept.WriteByte('\n')
			continue
		}
		if last[rowKey(e)] != i {
			removed++
			continue
		}
		kept.WriteString(line)
		kept.WriteByte('\n')
	}
	if removed == 0 {
		return 0, nil
	}
	if err := writeBankFile(path, kept.String()); err != nil {
		return 0, err
	}
	if err := m.reindexBankLocked(bank); err != nil {
		return removed, err
	}
	return removed, nil
}

// appendBank writes rows to the bank's JSONL, creating the banks dir on
// first use. Raw append, no cursor logic — callers own idempotency.
func (m *Memory) appendBank(bank string, entries []Entry) error {
	m.bankMu.Lock()
	defer m.bankMu.Unlock()
	return m.appendBankLocked(bank, entries)
}

func (m *Memory) appendBankLocked(bank string, entries []Entry) error {
	if err := os.MkdirAll(filepath.Join(m.root, "banks"), 0o755); err != nil {
		return fmt.Errorf("memory: create banks dir: %w", err)
	}
	// The dedupe runs BEFORE the append: a row written without a client
	// document id is identified by its body, so writing the same body again
	// REPLACES the row instead of appending a second copy of it. Rows the
	// client owns (an explicit metadata.document_id) are left alone — their
	// lifecycle is the client's (the Hindsight replace/append contract), and
	// the explicit replace path drops them.
	if err := m.dedupeBankLocked(bank, entries); err != nil {
		return err
	}
	f, err := os.OpenFile(m.bankPath(bank), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("memory: open bank %s: %w", bank, err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for i := range entries {
		e := entries[i]
		e.Content = Redact(e.Content)
		if e.Metadata == nil {
			// Share the map with the caller's entry, so the indexed row
			// carries the same content id the file does.
			entries[i].Metadata = map[string]any{}
			e.Metadata = entries[i].Metadata
		}
		if _, ok := e.Metadata["content_id"]; !ok {
			e.Metadata["content_id"] = contentDocumentID(e.Content)
		}
		if err := enc.Encode(e); err != nil {
			return fmt.Errorf("memory: encode entry: %w", err)
		}
	}
	return m.indexAppendLocked(bank, entries)
}

// dedupeBankLocked drops the existing rows the incoming entries supersede:
// every row of the same body that the client did not give a document id to.
//
// Rows with a client document id survive — the client owns their lifecycle
// (the Hindsight replace/append contract), and a chunked document keeps one
// document id across rows with DIFFERENT bodies. Only a row whose identity
// AND body match an incoming row is a duplicate of it.
func (m *Memory) dedupeBankLocked(bank string, entries []Entry) error {
	keys := make(map[string]bool, len(entries))
	for _, e := range entries {
		if entryDocumentID(e) != "" {
			continue // client-owned row: its replace/append contract applies
		}
		keys[rowKey(Entry{Content: Redact(e.Content)})] = true
	}
	if len(keys) == 0 {
		return nil
	}
	_, err := m.rewriteBankLocked(bank, func(e Entry) bool { return keys[rowKey(e)] })
	return err
}

// bankCursor is the highest retained_through_user_turn stored in the bank
// (0 for a missing/empty bank), kept in the index on every append (RS-20; it
// used to re-read the bank JSONL on every retain).
func (m *Memory) bankCursor(bank string) int {
	v, _ := m.cursor("bank", sanitizeBank(bank))
	return v
}

// Recall returns up to limit entries scored by query-token overlap from one
// bank. Zero-match entries never surface. limit <= 0 means 8 (OMP recall
// limit).
func (m *Memory) Recall(bank, query string, limit int) ([]Entry, error) {
	return m.RecallBanksCtx(context.Background(), []string{bank}, query, limit)
}

// RecallCtx is Recall with the caller's context (see SessionRetainCtx).
func (m *Memory) RecallCtx(ctx context.Context, bank, query string, limit int) ([]Entry, error) {
	return m.RecallBanksCtx(ctx, []string{bank}, query, limit)
}

// RecallBanks merges ranked recall across banks in order (Rust
// MemoryStore::recall, store.rs:150-188): rows are deduped by id, scored by
// unique-query-token overlap, zero-match rows never surface, and the merged
// set is ranked score-descending (stable — bank order breaks ties, matching
// the reference's append-then-sort). Missing banks read as empty.
func (m *Memory) RecallBanks(banks []string, query string, limit int) ([]Entry, error) {
	return m.RecallBanksCtx(context.Background(), banks, query, limit)
}

// RecallBanksCtx is RecallBanks with the caller's context (see
// SessionRetainCtx).
func (m *Memory) RecallBanksCtx(ctx context.Context, banks []string, query string, limit int) ([]Entry, error) {
	return m.RecallFilteredCtx(ctx, banks, query, limit, nil)
}

// RecallFiltered is RecallBanks with an eligibility filter applied BEFORE
// ranking and the limit, so a scope filter (hindsight tags) narrows the
// candidate set instead of an already-truncated page. A nil keep admits all.
func (m *Memory) RecallFiltered(banks []string, query string, limit int, keep func(Entry) bool) ([]Entry, error) {
	return m.RecallFilteredCtx(context.Background(), banks, query, limit, keep)
}

// RecallFilteredCtx is RecallFiltered with the caller's context: every recall
// ends here, so this is the one place a recall event is recorded.
func (m *Memory) RecallFilteredCtx(ctx context.Context, banks []string, query string, limit int, keep func(Entry) bool) ([]Entry, error) {
	if limit <= 0 {
		limit = 8
	}
	start := time.Now()
	// RS-18: bm25 over the index (porter stemming, accent folding, stopwords
	// dropped from the query) instead of raw unique-token overlap over a full
	// JSONL scan, where a row of stopwords outranked real content.
	rows, denseIDs, err := m.recallIndexed(banks, query, limit, keep)
	if err != nil {
		return nil, err
	}
	recordRecall(ctx, banks, query, limit, rows, denseIDs, start)
	return rows, nil
}

// recentBanks returns the newest limit rows across the given banks — the
// cold-snapshot read when there is no query to rank on (FirstTurnMemories'
// empty-query case). Rows are deduped by id in merge order (project before
// shared), ranked newest-first with stable bank/file order on equal
// timestamps; empty-content rows are skipped (never recallable).
func (m *Memory) recentBanks(banks []string, limit int) ([]Entry, error) {
	rows, _, err := m.recentBanksCounting(banks, limit)
	return rows, err
}

// recentBanksCounting is recentBanks that also reports how many rows the
// banks hold. The cold path already parses every line of every file to sort
// them, so the count is free — and it is what makes an injection block able
// to say "22 more memories hidden" instead of silently showing a prefix.
func (m *Memory) recentBanksCounting(banks []string, limit int) ([]Entry, int, error) {
	var rows []Entry
	seenIDs := map[string]bool{}
	for _, bank := range banks {
		data, err := os.ReadFile(m.bankPath(bank))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, 0, err
		}
		for _, line := range strings.Split(string(data), "\n") {
			if line == "" {
				continue
			}
			var e Entry
			if json.Unmarshal([]byte(line), &e) != nil {
				continue
			}
			if seenIDs[e.ID] || strings.TrimSpace(e.Content) == "" {
				continue
			}
			seenIDs[e.ID] = true
			rows = append(rows, e)
		}
	}
	total := len(rows)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Timestamp > rows[j].Timestamp })
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, total, nil
}

// InjectBlock renders ranked recall rows as the <memories>-equivalent
// injection block (PRD FR-ZCP-07 recall/injection contract,
// mnemopi/state.ts:914-922): one `- ` line per row, caller's rank order
// preserved, capped to limit entries and a total token budget. When the
// budget is exhausted mid-entry the remaining entries are dropped whole
// (never a truncated half-memory). An empty result returns "" so callers
// skip the block entirely.
func InjectBlock(entries []Entry, limit, tokenBudget int) string {
	text, _ := injectRows(entries, limit, tokenBudget, len(entries))
	return text
}

// injectRows is InjectBlock that also reports how many leading entries the
// block carries (the rows telemetry counts as injected).
func injectRows(entries []Entry, limit, tokenBudget, total int) (string, int) {
	if limit <= 0 {
		limit = 8 // OMP recallLimit
	}
	if tokenBudget <= 0 {
		tokenBudget = 5000 // OMP injectionTokenLimit
	}
	var b strings.Builder
	b.WriteString("<memories>\n")
	used := 0
	n := 0
	for _, e := range entries {
		if n == limit {
			break
		}
		line := "- " + e.Content
		cost := (len(line) + 1) / 4 // compress.EstimateTokens shape: bytes/4
		if used+cost > tokenBudget {
			break
		}
		b.WriteString(line)
		b.WriteString("\n")
		used += cost
		n++
	}
	if n == 0 {
		return "", 0
	}
	// FR-SMA-06 (truncation half): a budget is useless if it is invisible.
	// The reader of this block cannot see the banks it came from, so it has
	// no way to know it is looking at a PREFIX. Measured: 30 rows retained,
	// 8 injected, and the block was silent about the other 22 — an agent
	// reading it reasonably concluded that was the whole memory. A comment
	// line inside the block is safe for the callers that render it as text
	// and for an XML consumer it parses as a comment, so nothing has to
	// strip it.
	hidden := total - n
	if hidden <= 0 {
		hidden = len(entries) - n
	}
	if hidden > 0 {
		fmt.Fprintf(&b, "\n<!-- %d more memories hidden by the injection budget of %d entries / %d tokens -->\n", hidden, limit, tokenBudget)
	}
	b.WriteString("</memories>")
	return b.String(), n
}

// RankedMemory is one row of the OMP recall/injection contract
// {id, content, source, timestamp, score} (Rust handler.rs:3031-3041).
// Score is the row's relative relevance in its recall (see Entry.Score):
// non-increasing down the list, meaningful only within one response. The
// list order is the authoritative rank; OMP does not consume the value.
type RankedMemory struct {
	ID        string  `json:"id"`
	Content   string  `json:"content"`
	Source    string  `json:"source"`
	Timestamp int64   `json:"timestamp"`
	Score     float64 `json:"score"`
}

// RankEntries renders recall rows into the OMP injection contract shape.
func RankEntries(entries []Entry) []RankedMemory {
	out := make([]RankedMemory, 0, len(entries))
	for _, e := range entries {
		out = append(out, RankedMemory{ID: e.ID, Content: e.Content, Source: e.Source, Timestamp: e.Timestamp, Score: e.Score})
	}
	return out
}

// tokenize lowercases and splits on non-letter/digit runes.
func tokenize(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// entryMetadata builds a session_retain row's metadata. Tags are first-class
// (K2): a caller passes them explicitly and they are persisted under the same
// "tags" key the REST retain path and the Hindsight tag filters read, so a
// tagged row is recallable by tag no matter which surface wrote it. An empty
// tag list leaves the key absent rather than writing an empty array, which the
// filters read as "untagged" (and "exact" then selects these rows).
func entryMetadata(sessionID, sourceID string, messageCount, throughUserTurn int, cwd string, tags []string) map[string]any {
	meta := map[string]any{
		"session_id":                 sessionID,
		"source_id":                  sourceID,
		"message_count":              messageCount,
		"retained_through_user_turn": throughUserTurn,
		"cwd":                        cwd,
	}
	if len(tags) > 0 {
		meta["tags"] = append([]string(nil), tags...)
	}
	return meta
}
