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

// Retain appends a transcript batch to a bank as JSONL, tagging every row's
// metadata with the retained_through_user_turn cursor. Idempotent: when
// throughUserTurn is at or below the bank's stored cursor the write is
// skipped entirely (resume-safety; the Rust side keys this per session, the
// Go contract keys it per bank).
func (m *Memory) Retain(bank string, entries []Entry, throughUserTurn int) error {
	if err := os.MkdirAll(filepath.Join(m.root, "banks"), 0o755); err != nil {
		return fmt.Errorf("memory: create banks dir: %w", err)
	}
	if throughUserTurn <= m.bankCursor(bank) {
		return nil
	}
	path := m.bankPath(bank)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("memory: open bank %s: %w", bank, err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, e := range entries {
		if e.Metadata == nil {
			e.Metadata = map[string]any{}
		}
		e.Metadata["retained_through_user_turn"] = throughUserTurn
		if e.ID == "" {
			e.ID = fmt.Sprintf("%d", time.Now().UnixNano())
		}
		if e.Source == "" {
			e.Source = "coding-agent-transcript"
		}
		if e.Timestamp == 0 {
			e.Timestamp = time.Now().Unix()
		}
		if e.Importance == 0 {
			e.Importance = 0.65
		}
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

// Recall returns up to limit entries scored by query-token overlap.
// Zero-match entries never surface. limit <= 0 means 8 (OMP recall limit).
func (m *Memory) Recall(bank, query string, limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = 8
	}
	data, err := os.ReadFile(m.bankPath(bank))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
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
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var e Entry
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
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

// tokenize lowercases and splits on non-letter/digit runes.
func tokenize(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}
