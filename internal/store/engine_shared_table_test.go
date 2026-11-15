package store

import (
	"errors"
	"reflect"
	"slices"
	"testing"
)

// Table tests for the engine-neutral helpers in engine_shared.go. Each expected
// value was read off the per-engine code that CX-51 replaced (SQLite and
// PostgreSQL behaved the same on every row here), so a change in either engine
// shows up as a failure in one shared table.

func TestEncodeJSONObject(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want string
	}{
		{"nil stores the empty object", nil, "{}"},
		{"empty map stores the empty object", map[string]any{}, "{}"},
		{"single key", map[string]any{"a": 1}, `{"a":1}`},
		{"keys are sorted", map[string]any{"b": "x", "a": true}, `{"a":true,"b":"x"}`},
		{"unicode is kept", map[string]any{"k": "é"}, `{"k":"é"}`},
		{"unmarshalable value falls back to the empty object", map[string]any{"f": func() {}}, "{}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := encodeJSONObject(c.in); got != c.want {
				t.Errorf("encodeJSONObject = %q; want %q", got, c.want)
			}
		})
	}
}

func TestDecodeJSONObject(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want map[string]any
	}{
		{"empty string reads as nil", "", nil},
		{"empty object reads as nil", "{}", nil},
		{"object decodes", `{"a":1}`, map[string]any{"a": float64(1)}},
		{"malformed reads as nil", "not json", nil},
		{"array is not an object", "[1]", nil},
		{"null reads as nil", "null", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decodeJSONObject(c.in); !reflect.DeepEqual(got, c.want) {
				t.Errorf("decodeJSONObject(%q) = %v; want %v", c.in, got, c.want)
			}
		})
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	m := map[string]any{"kind": "handler", "n": float64(2), "nested": map[string]any{"ok": true}}
	if got := decodeJSONObject(encodeJSONObject(m)); !reflect.DeepEqual(got, m) {
		t.Errorf("round trip = %v; want %v", got, m)
	}
}

func TestKeepRareNameTokens(t *testing.T) {
	cases := []struct {
		name   string
		toks   []string
		counts map[string]int
		bound  int
		want   []string
	}{
		{"a token at the bound stays, above it is dropped",
			[]string{"quartz", "widget"}, map[string]int{"quartz": 2, "widget": 3}, 2, []string{"quartz"}},
		{"bound 0 drops every token seen at least once",
			[]string{"alpha", "beta"}, map[string]int{"alpha": 1}, 0, []string{"beta"}},
		{"a token missing from counts reads as zero and stays",
			[]string{"gamma"}, map[string]int{}, 2, []string{"gamma"}},
		{"no tokens", nil, map[string]int{"x": 1}, 2, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			toks := slices.Clone(c.toks)
			if got := keepRareNameTokens(toks, c.counts, c.bound); !slices.Equal(got, c.want) {
				t.Errorf("keepRareNameTokens = %v; want %v", got, c.want)
			}
		})
	}
}

func TestPrepareStaleSweep(t *testing.T) {
	t.Run("read-only handle refuses the sweep", func(t *testing.T) {
		if _, err := prepareStaleSweep(RO, []string{"a"}); !errors.Is(err, ErrReadOnly) {
			t.Errorf("err = %v; want ErrReadOnly", err)
		}
	})
	cases := []struct {
		name string
		keep []string
		want []string
	}{
		{"nil keep is empty", nil, nil},
		{"blank entries are dropped", []string{" ", ""}, nil},
		{"entries are trimmed and de-duplicated in order", []string{"b", " a ", "a", "b"}, []string{"b", "a"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := prepareStaleSweep(RW, c.keep)
			if err != nil || !slices.Equal(got, c.want) {
				t.Errorf("prepareStaleSweep(RW) = %v, err %v; want %v", got, err, c.want)
			}
		})
	}
}

func TestMigrateModeError(t *testing.T) {
	if err := migrateModeError(RW); err != nil {
		t.Errorf("RW: err = %v; want nil", err)
	}
	err := migrateModeError(RO)
	if err == nil || err.Error() != "store: migrate requires RW mode" {
		t.Errorf("RO: err = %v; want %q", err, "store: migrate requires RW mode")
	}
}

func TestFuseHybridHitsMatchesFusion(t *testing.T) {
	lists := []RankList{
		{Name: ArmVector, Keys: []string{"x", "y", "z"}},
		{Name: ArmName, Keys: []string{"y", "w"}},
	}
	w := DefaultFuseWeights()
	similarity := map[string]float64{"x": 0.1, "y": 0.2, "z": 0.3, "w": 0.4}
	hit := func(qn string) HybridHit {
		return HybridHit{Element: Element{QualifiedName: qn}, Similarity: similarity[qn]}
	}
	full := FuseRRFWeighted(lists, w)
	for _, limit := range []int{1, 2, 10} {
		want := full
		if len(want) > limit {
			want = want[:limit]
		}
		got := fuseHybridHits(lists, w, limit, hit)
		if len(got) != len(want) {
			t.Fatalf("limit %d: %d hits; want %d", limit, len(got), len(want))
		}
		for i := range want {
			if got[i].Element.QualifiedName != want[i].Key || got[i].Score != want[i].Score ||
				!reflect.DeepEqual(got[i].Ranks, want[i].Ranks) || got[i].Similarity != similarity[want[i].Key] {
				t.Errorf("limit %d hit %d = %+v; want key %s score %v ranks %v similarity %v",
					limit, i, got[i], want[i].Key, want[i].Score, want[i].Ranks, similarity[want[i].Key])
			}
		}
	}
}
