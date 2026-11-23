package store

import (
	"context"
	"testing"
)

// A store that was never embedded can lack the emb_stamp table (a read-only
// open of a store whose schema never carried it). That is a legitimate
// "nothing is embedded" state, not a failed read: the stamp listing must be
// empty, not an error, so status does not report the store unhealthy (#592).
func TestStampsAbsentTableIsEmpty(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.db.Exec(`DROP TABLE emb_stamp`); err != nil {
		t.Fatalf("drop emb_stamp: %v", err)
	}
	stamps, err := s.Stamps(ctx)
	if err != nil || len(stamps) != 0 {
		t.Fatalf("Stamps without emb_stamp = %+v, %v; want empty and no error", stamps, err)
	}
	st, err := s.Stamp(ctx, "m-1")
	if err != nil || st != nil {
		t.Fatalf("Stamp without emb_stamp = %+v, %v; want nil and no error", st, err)
	}
}
