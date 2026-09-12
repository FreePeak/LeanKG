package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// auditDetailsJSON canonicalizes the details map for hashing.
func auditDetailsJSON(e *AuditEntry) string {
	if e.Details == nil {
		return "{}"
	}
	keys := make([]string, 0, len(e.Details))
	for k := range e.Details {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	b, _ := json.Marshal(e.Details)
	return string(b)
}

func auditHash(prev string, e *AuditEntry) string {
	h := sha256.Sum256([]byte(prev + "|" +
		fmt.Sprintf("%d", e.At) + "|" + e.Actor + "|" + e.Action + "|" + e.Target + "|" +
		auditDetailsJSON(e)))
	return hex.EncodeToString(h[:])
}

// AppendAudit writes one hash-chained record. The chain head is the row with
// the highest seq; the single write connection serializes concurrent appends.
func (s *Store) AppendAudit(e *AuditEntry) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var seq int64
	var prev string
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq),0), COALESCE((SELECT hash FROM audit_ledger ORDER BY seq DESC LIMIT 1),'') FROM audit_ledger`).Scan(&seq, &prev); err != nil {
		return err
	}
	e.Seq = seq + 1
	e.PrevHash = prev
	if e.At == 0 {
		e.At = time.Now().Unix()
	}
	e.Hash = auditHash(e.PrevHash, e)
	if _, err := tx.Exec(`INSERT INTO audit_ledger (seq, at, actor, action, target, details, prev_hash, hash)
		VALUES (?,?,?,?,?,?,?,?)`, e.Seq, e.At, e.Actor, e.Action, e.Target, auditDetailsJSON(e), e.PrevHash, e.Hash); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.BumpWatermark()
}

// AuditTail returns the last `limit` entries in ascending seq order.
func (s *Store) AuditTail(limit int) ([]AuditEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT seq, at, actor, action, target, details, prev_hash, hash FROM audit_ledger
		ORDER BY seq DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var details string
		if err := rows.Scan(&e.Seq, &e.At, &e.Actor, &e.Action, &e.Target, &details, &e.PrevHash, &e.Hash); err != nil {
			return nil, err
		}
		if details != "" && details != "{}" {
			_ = json.Unmarshal([]byte(details), &e.Details)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// VerifyAuditChain walks the whole ledger recomputing hashes; the first
// mismatch (prev-link or entry hash) reports brokenAt.
func (s *Store) VerifyAuditChain() (ok bool, brokenAt int64, err error) {
	rows, err := s.db.Query(`SELECT seq, at, actor, action, target, details, prev_hash, hash FROM audit_ledger ORDER BY seq`)
	if err != nil {
		return false, 0, err
	}
	defer rows.Close()
	prev := ""
	for rows.Next() {
		var e AuditEntry
		var details string
		if err := rows.Scan(&e.Seq, &e.At, &e.Actor, &e.Action, &e.Target, &details, &e.PrevHash, &e.Hash); err != nil {
			return false, 0, err
		}
		if details != "" && details != "{}" {
			_ = json.Unmarshal([]byte(details), &e.Details)
		}
		if e.PrevHash != prev {
			return false, e.Seq, nil
		}
		if e.Hash != auditHash(e.PrevHash, &e) {
			return false, e.Seq, nil
		}
		prev = e.Hash
	}
	return true, 0, rows.Err()
}

// Engine names the storage engine of this handle.
func (s *Store) Engine() string { return EngineSQLite }

// compile-time proof (backend.go declares it too; one is enough — keep the
// check adjacent to the audit methods so new Backend methods fail here fast).
var (
	_ = func() error {
		var b Backend = (*Store)(nil)
		if b == nil {
			return fmt.Errorf("unreachable")
		}
		return nil
	}
)
