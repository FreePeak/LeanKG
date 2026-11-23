package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// watermarkFails is a real store whose watermark read fails. Status reads the
// watermark and fails the whole call with it; the import's inventory refresh
// does not read it, so the index itself succeeds.
type watermarkFails struct {
	store.Backend
	cause error
}

func (s watermarkFails) Watermark(ctx context.Context) (int64, int64, error) {
	return 0, 0, s.cause
}

// TestImportIndexNamesStatusFailure: the index writes its rows, then the status
// that follows it fails. The import used to drop that error (`status, _ :=`),
// so the answer carried a null status and nothing said why. The failure must
// be named in the payload; the index result is still delivered.
func TestImportIndexNamesStatusFailure(t *testing.T) {
	dir := writeProject(t)
	base := engineAt(t, dir).st
	cause := errors.New("watermark read failed")
	e := New(watermarkFails{Backend: base, cause: cause}, Options{ProjectDir: dir})

	out, err := e.Import(context.Background(), ImportRequest{Action: "repo", Path: dir})
	if err != nil {
		t.Fatalf("index succeeded, import must still answer: %v", err)
	}
	msg, _ := out["status_error"].(string)
	if !strings.Contains(msg, cause.Error()) {
		t.Fatalf("status_error = %q, want it to name %q; payload: %v", msg, cause, out)
	}
	indexed, ok := out["indexed"].(map[string]any)
	if !ok || indexed["files"] == 0 || indexed["files"] == nil {
		t.Fatalf("index result missing from payload: %v", out["indexed"])
	}
}
