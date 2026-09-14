package memory

import (
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
func (m *Memory) SessionRetain(scope Scope, cwd, bank, sessionID string, turns []string, throughUserTurn int) (RetainResult, error) {
	if sessionID == "" {
		return RetainResult{}, fmt.Errorf("memory: session_retain requires session_id")
	}
	if len(turns) == 0 {
		return RetainResult{}, fmt.Errorf("memory: session_retain requires turns (array of transcript turn texts)")
	}
	if throughUserTurn < 0 {
		return RetainResult{}, fmt.Errorf("memory: retained_through_user_turn must be >= 0")
	}
	target := bank
	if target == "" {
		target = scope.WriteBank(BankName(m.scopeCwd(cwd)))
	}
	if seen, ok := m.sessionCursor(sessionID); ok && throughUserTurn <= seen {
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
			Metadata: map[string]any{
				"session_id":                 sessionID,
				"source_id":                  sourceID,
				"message_count":              len(turns),
				"retained_through_user_turn": throughUserTurn,
				"cwd":                        cwd,
			},
		})
	}
	if err := m.appendBank(target, entries); err != nil {
		return RetainResult{}, err
	}
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
	banks = m.sessionReadBanks(scope, cwd, bank)
	entries, err = m.RecallBanks(banks, query, limit)
	return banks, entries, err
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
	files, err := os.ReadDir(filepath.Join(m.root, "banks"))
	if err != nil {
		return 0, false
	}
	best, found := 0, false
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(m.root, "banks", f.Name()))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if line == "" {
				continue
			}
			var e Entry
			if json.Unmarshal([]byte(line), &e) != nil {
				continue
			}
			if e.Metadata["session_id"] != sessionID {
				continue
			}
			if n, ok := e.Metadata["retained_through_user_turn"].(float64); ok && (!found || int(n) > best) {
				best, found = int(n), true
			}
		}
	}
	return best, found
}

// Retain appends a transcript batch to a bank as JSONL, tagging every row's
// metadata with the retained_through_user_turn cursor. Idempotent per bank:
// when throughUserTurn is at or below the bank's stored cursor the write is
// skipped entirely (resume-safety; the REST/mnemopi contract keys it per
// bank — SessionRetain below keys the same check per session, matching the
// reference handler).
func (m *Memory) Retain(bank string, entries []Entry, throughUserTurn int) error {
	if throughUserTurn <= m.bankCursor(bank) {
		return nil
	}
	for i := range entries {
		if entries[i].Metadata == nil {
			entries[i].Metadata = map[string]any{}
		}
		entries[i].Metadata["retained_through_user_turn"] = throughUserTurn
	}
	applyEntryDefaults(entries)
	return m.appendBank(bank, entries)
}

// RetainRaw appends entries WITHOUT the user-turn cursor gate: same
// id/source/timestamp/importance defaults as Retain, but every call writes.
// The hindsight-compat mount (#414) uses this — the Hindsight retain wire has
// no retained_through_user_turn concept, and routing it through Retain's
// cursor would silently drop every write after the first (a missing cursor
// is 0, and 0 <= 0 short-circuits).
func (m *Memory) RetainRaw(bank string, entries []Entry) error {
	applyEntryDefaults(entries)
	return m.appendBank(bank, entries)
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

// appendBank writes rows to the bank's JSONL, creating the banks dir on
// first use. Raw append, no cursor logic — callers own idempotency.
func (m *Memory) appendBank(bank string, entries []Entry) error {
	if err := os.MkdirAll(filepath.Join(m.root, "banks"), 0o755); err != nil {
		return fmt.Errorf("memory: create banks dir: %w", err)
	}
	f, err := os.OpenFile(m.bankPath(bank), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("memory: open bank %s: %w", bank, err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			return fmt.Errorf("memory: encode entry: %w", err)
		}
	}
	return nil
}

// bankCursor reads the highest retained_through_user_turn stored in the
// bank's JSONL (0 for a missing/empty bank).
func (m *Memory) bankCursor(bank string) int {
	data, err := os.ReadFile(m.bankPath(bank))
	if err != nil {
		return 0
	}
	max := 0
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var e Entry
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		if n, ok := e.Metadata["retained_through_user_turn"].(float64); ok && int(n) > max {
			max = int(n)
		}
	}
	return max
}

// Recall returns up to limit entries scored by query-token overlap from one
// bank. Zero-match entries never surface. limit <= 0 means 8 (OMP recall
// limit).
func (m *Memory) Recall(bank, query string, limit int) ([]Entry, error) {
	return m.RecallBanks([]string{bank}, query, limit)
}

// RecallBanks merges ranked recall across banks in order (Rust
// MemoryStore::recall, store.rs:150-188): rows are deduped by id, scored by
// unique-query-token overlap, zero-match rows never surface, and the merged
// set is ranked score-descending (stable — bank order breaks ties, matching
// the reference's append-then-sort). Missing banks read as empty.
func (m *Memory) RecallBanks(banks []string, query string, limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = 8
	}
	// unique query tokens, lowercased
	qSeen := map[string]bool{}
	var qTokens []string
	for _, t := range tokenize(query) {
		if !qSeen[t] {
			qSeen[t] = true
			qTokens = append(qTokens, t)
		}
	}
	if len(qTokens) == 0 {
		return nil, nil
	}
	type scored struct {
		e     Entry
		score int
	}
	var matched []scored
	seenIDs := map[string]bool{}
	for _, bank := range banks {
		data, err := os.ReadFile(m.bankPath(bank))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, line := range strings.Split(string(data), "\n") {
			if line == "" {
				continue
			}
			var e Entry
			if json.Unmarshal([]byte(line), &e) != nil {
				continue
			}
			if seenIDs[e.ID] {
				continue
			}
			seenIDs[e.ID] = true
			seen := map[string]bool{}
			for _, t := range tokenize(e.Content) {
				seen[t] = true
			}
			score := 0
			for _, t := range qTokens {
				if seen[t] {
					score++
				}
			}
			if score == 0 {
				continue // zero-match entries never surface
			}
			matched = append(matched, scored{e, score})
		}
	}
	if len(matched) == 0 {
		return nil, nil
	}
	sort.SliceStable(matched, func(i, j int) bool { return matched[i].score > matched[j].score })
	entries := make([]Entry, 0, min(limit, len(matched)))
	for _, s := range matched {
		if len(entries) == limit {
			break
		}
		entries = append(entries, s.e)
	}
	return entries, nil
}

// recentBanks returns the newest limit rows across the given banks — the
// cold-snapshot read when there is no query to rank on (FirstTurnMemories'
// empty-query case). Rows are deduped by id in merge order (project before
// shared), ranked newest-first with stable bank/file order on equal
// timestamps; empty-content rows are skipped (never recallable).
func (m *Memory) recentBanks(banks []string, limit int) ([]Entry, error) {
	var rows []Entry
	seenIDs := map[string]bool{}
	for _, bank := range banks {
		data, err := os.ReadFile(m.bankPath(bank))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
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
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Timestamp > rows[j].Timestamp })
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

// InjectBlock renders ranked recall rows as the <memories>-equivalent
// injection block (PRD FR-ZCP-07 recall/injection contract,
// mnemopi/state.ts:914-922): one `- ` line per row, caller's rank order
// preserved, capped to limit entries and a total token budget. When the
// budget is exhausted mid-entry the remaining entries are dropped whole
// (never a truncated half-memory). An empty result returns "" so callers
// skip the block entirely.
func InjectBlock(entries []Entry, limit, tokenBudget int) string {
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
		return ""
	}
	b.WriteString("</memories>")
	return b.String()
}

// RankedMemory is one row of the OMP recall/injection contract
// {id, content, source, timestamp, score} (Rust handler.rs:3031-3041).
// Score is the reference's constant 0.0 — the rank is carried by the list
// order and OMP does not consume the value.
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
		out = append(out, RankedMemory{ID: e.ID, Content: e.Content, Source: e.Source, Timestamp: e.Timestamp})
	}
	return out
}

// tokenize lowercases and splits on non-letter/digit runes.
func tokenize(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}
