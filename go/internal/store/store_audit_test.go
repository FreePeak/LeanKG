package store

import "testing"

func TestAuditLedgerChain(t *testing.T) {
	s := openTestStore(t)
	for i, action := range []string{"import", "query", "import", "status"} {
		if err := s.AppendAudit(&AuditEntry{Actor: "test", Action: action, Target: "t1", Details: map[string]any{"i": i}}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	tail, err := s.AuditTail(4)
	if err != nil || len(tail) != 4 {
		t.Fatalf("tail: %v len=%d", err, len(tail))
	}
	for i, want := range []string{"import", "query", "import", "status"} {
		if tail[i].Action != want {
			t.Fatalf("tail[%d] = %q, want %q (ascending seq)", i, tail[i].Action, want)
		}
		if tail[i].Seq != int64(i+1) {
			t.Fatalf("tail[%d].Seq = %d", i, tail[i].Seq)
		}
	}
	if tail[2].PrevHash != tail[1].Hash {
		t.Fatalf("chain link broken: %s != %s", tail[2].PrevHash, tail[1].Hash)
	}
	ok, brokenAt, err := s.VerifyAuditChain()
	if err != nil || !ok || brokenAt != 0 {
		t.Fatalf("verify: ok=%v brokenAt=%d err=%v", ok, brokenAt, err)
	}

	// Tamper with one row's target: the chain must break exactly there.
	if _, err := s.db.Exec(`UPDATE audit_ledger SET target='tampered' WHERE seq=2`); err != nil {
		t.Fatal(err)
	}
	ok, brokenAt, err = s.VerifyAuditChain()
	if err != nil {
		t.Fatal(err)
	}
	if ok || brokenAt != 2 {
		t.Fatalf("tampered ledger: ok=%v brokenAt=%d, want false @2", ok, brokenAt)
	}
}

func TestBackendInterfaceSatisfied(t *testing.T) {
	s := openTestStore(t)
	var b Backend = s
	if b.Engine() != "sqlite" {
		t.Fatalf("engine = %q", b.Engine())
	}
	if _, err := b.Elements(); err != nil {
		t.Fatalf("elements: %v", err)
	}
	if c, o, err := b.VectorCoverage("none"); err != nil || c != 0 || o != 0 {
		t.Fatalf("coverage: %d %d %v", c, o, err)
	}
}
