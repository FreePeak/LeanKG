package embed

import (
	"context"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// kvFake embeds the package Store as nil; only KVSet and KVGet are real, so the
// calibration round trip proves it needs the KV role and nothing else.
type kvFake struct {
	Store
	kv map[string]string
}

func (f *kvFake) KVSet(_ context.Context, ns, key, value string) error {
	f.kv[ns+"/"+key] = value
	return nil
}

func (f *kvFake) KVGet(_ context.Context, ns, key string) (string, bool, error) {
	v, ok := f.kv[ns+"/"+key]
	return v, ok, nil
}

func TestCalibrationRoundTripsThroughKVRole(t *testing.T) {
	ctx := context.Background()
	f := &kvFake{kv: map[string]string{}}
	stamp := store.ModelStamp{ModelID: "model-1"}
	if err := WriteCalibration(ctx, f, stamp, 0.42); err != nil {
		t.Fatalf("WriteCalibration: %v", err)
	}
	floor, ok := ReadCalibration(ctx, f, stamp)
	if !ok || floor != 0.42 {
		t.Fatalf("ReadCalibration = %v, %v; want 0.42, true", floor, ok)
	}
}
