package all

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/internal/sessionlink"
)

// TestAdaptersHonourCancelledCtx pins the cancellation entry points of every
// adapter: with a cancelled ctx, Locate, Window and Transcript return the
// context error, never ErrNotFound or a partial result. Cancellation is not
// a data failure, so the runner must not count it as one.
func TestAdaptersHonourCancelledCtx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	home := t.TempDir()
	missing := sessionlink.TranscriptRef{Path: filepath.Join(home, "missing.jsonl")}

	for _, a := range Adapters() {
		name := a.Client()
		if _, _, err := a.Locate(ctx, home, sessionlink.CallRef{ClientSessionID: "s", Cwd: home}, nil); !errors.Is(err, context.Canceled) {
			t.Errorf("%s Locate: err = %v, want context.Canceled", name, err)
		}
		if _, err := a.Window(ctx, missing, sessionlink.CallRef{Cwd: home}, 2, nil); !errors.Is(err, context.Canceled) {
			t.Errorf("%s Window: err = %v, want context.Canceled", name, err)
		}
		if _, err := a.Transcript(ctx, missing, 4); !errors.Is(err, context.Canceled) {
			t.Errorf("%s Transcript: err = %v, want context.Canceled", name, err)
		}
	}
}
