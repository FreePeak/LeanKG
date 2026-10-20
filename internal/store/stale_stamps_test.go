package store

import "testing"

// seedCollection writes one model's stamp, vectors and embedding state, so a
// test can assert on a collection as a whole (the three tables travel
// together: a stamp without vectors is a collection the L3 rung would select
// and then find empty).
func seedCollection(t *testing.T, s *Store, modelID string, qns []string) {
	t.Helper()
	if err := s.WriteStamp(ModelStamp{
		ModelID: modelID, Revision: "rev-" + modelID, Dimensions: 4,
		Distance: "cosine", Provider: "local",
	}); err != nil {
		t.Fatalf("write stamp %s: %v", modelID, err)
	}
	rows := make([]VectorRow, 0, len(qns))
	states := map[string]string{}
	for _, qn := range qns {
		rows = append(rows, VectorRow{QualifiedName: qn, Vec: []float32{1, 0, 0, 0}})
		states[qn] = "hash-" + qn
	}
	if err := s.UpsertVectors(modelID, rows); err != nil {
		t.Fatalf("upsert vectors %s: %v", modelID, err)
	}
	if err := s.SetEmbeddingStates(modelID, states); err != nil {
		t.Fatalf("set states %s: %v", modelID, err)
	}
}

func collectionCounts(t *testing.T, s *Store, modelID string) (vectors, states int, stamped bool) {
	t.Helper()
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM embedding_vectors WHERE model_id = ?`, modelID).Scan(&vectors); err != nil {
		t.Fatalf("count vectors %s: %v", modelID, err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM embedding_state WHERE model_id = ?`, modelID).Scan(&states); err != nil {
		t.Fatalf("count states %s: %v", modelID, err)
	}
	stamp, err := s.Stamp(modelID)
	if err != nil {
		t.Fatalf("stamp %s: %v", modelID, err)
	}
	return vectors, states, stamp != nil
}

func TestDeleteStaleStampsDropsOnlyUnkeptCollections(t *testing.T) {
	s := openTestStore(t)
	seedCollection(t, s, "live", []string{"a.Foo", "a.Bar"})
	seedCollection(t, s, "old", []string{"a.Baz"})

	sw, err := s.DeleteStaleStamps([]string{"live"})
	if err != nil {
		t.Fatalf("DeleteStaleStamps: %v", err)
	}
	if sw.Models != 1 || sw.Vectors != 1 || sw.State != 1 {
		t.Fatalf("sweep = %+v, want 1 model / 1 vector / 1 state", sw)
	}
	if v, st, stamped := collectionCounts(t, s, "live"); v != 2 || st != 2 || !stamped {
		t.Fatalf("live collection = %d vectors, %d states, stamped=%v; want 2/2/true", v, st, stamped)
	}
	if v, st, stamped := collectionCounts(t, s, "old"); v != 0 || st != 0 || stamped {
		t.Fatalf("old collection = %d vectors, %d states, stamped=%v; want 0/0/false", v, st, stamped)
	}
}

func TestDeleteStaleStampsWithNoKeepIsANoOp(t *testing.T) {
	// The caller that does not know which collection is live (a server
	// with no embedder wired) must not be able to delete one.
	for _, keep := range [][]string{nil, {}, {""}, {"  ", ""}} {
		s := openTestStore(t)
		seedCollection(t, s, "live", []string{"a.Foo"})
		seedCollection(t, s, "old", []string{"a.Baz"})

		sw, err := s.DeleteStaleStamps(keep)
		if err != nil {
			t.Fatalf("DeleteStaleStamps(%v): %v", keep, err)
		}
		if !sw.Empty() {
			t.Fatalf("DeleteStaleStamps(%v) = %+v, want an empty sweep", keep, sw)
		}
		for _, id := range []string{"live", "old"} {
			if v, st, stamped := collectionCounts(t, s, id); v != 1 || st != 1 || !stamped {
				t.Fatalf("keep=%v: collection %s = %d vectors, %d states, stamped=%v; want 1/1/true",
					keep, id, v, st, stamped)
			}
		}
	}
}

func TestDeleteStaleStampsKeepsEveryListedCollection(t *testing.T) {
	s := openTestStore(t)
	seedCollection(t, s, "a", []string{"a.Foo"})
	seedCollection(t, s, "b", []string{"a.Baz"})
	seedCollection(t, s, "c", []string{"a.Qux"})

	sw, err := s.DeleteStaleStamps([]string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("DeleteStaleStamps: %v", err)
	}
	if !sw.Empty() {
		t.Fatalf("sweep = %+v, want an empty sweep", sw)
	}
	for _, id := range []string{"a", "b", "c"} {
		if v, _, stamped := collectionCounts(t, s, id); v != 1 || !stamped {
			t.Fatalf("collection %s = %d vectors, stamped=%v; want 1/true", id, v, stamped)
		}
	}
}

// TestDeleteStaleStampsKeepsACollectionTheStoreDoesNotHaveTheKeepFor is the
// case that bit the first fleet run: the store was embedded under model id
// "local" (the old default), the server's embedder says "embeddinggemma-2",
// and the sweep dropped the store's ONLY collection — 22,064 vectors of embed
// work destroyed, for a store whose L3 was already degraded.
//
// A store whose keep collection is ABSENT is BEHIND, not STALE. Its one
// collection is what the operator needs (or has to re-embed) either way, so
// the sweep must not touch it.
func TestDeleteStaleStampsKeepsACollectionTheStoreDoesNotHaveTheKeepFor(t *testing.T) {
	s := openTestStore(t)
	seedCollection(t, s, "local", []string{"a.Foo"})

	sw, err := s.DeleteStaleStamps([]string{"embeddinggemma-2"})
	if err != nil {
		t.Fatalf("DeleteStaleStamps: %v", err)
	}
	if !sw.Empty() {
		t.Fatalf("sweep = %+v, want an empty sweep", sw)
	}
	if v, st, stamped := collectionCounts(t, s, "local"); v != 1 || st != 1 || !stamped {
		t.Fatalf("collection local = %d vectors, %d states, stamped=%v; want 1/1/true", v, st, stamped)
	}
}

// TestDeleteStaleStampsDropsTheOldCollectionWhenTheKeepOneExists is the
// other half: once the store HAS the keep collection, the old one is the
// dead weight the sweep exists to remove.
func TestDeleteStaleStampsDropsTheOldCollectionWhenTheKeepOneExists(t *testing.T) {
	s := openTestStore(t)
	seedCollection(t, s, "local", []string{"a.Foo"})
	seedCollection(t, s, "embeddinggemma-2", []string{"a.Bar"})

	sw, err := s.DeleteStaleStamps([]string{"embeddinggemma-2"})
	if err != nil {
		t.Fatalf("DeleteStaleStamps: %v", err)
	}
	if sw.Models != 1 || sw.Vectors != 1 {
		t.Fatalf("sweep = %+v, want 1 model / 1 vector dropped", sw)
	}
	if v, _, stamped := collectionCounts(t, s, "local"); v != 0 || stamped {
		t.Fatalf("collection local = %d vectors, stamped=%v; want 0/false", v, stamped)
	}
	if v, _, stamped := collectionCounts(t, s, "embeddinggemma-2"); v != 1 || !stamped {
		t.Fatalf("keep collection = %d vectors, stamped=%v; want 1/true", v, stamped)
	}
}
