package telemetry

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// homeDir returns a LEANKG_HOME that does not exist yet, and points HOME at a
// scratch dir so nothing can touch the real ~/.leankg.
func homeDir(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	home := filepath.Join(t.TempDir(), "lk")
	t.Setenv("LEANKG_HOME", home)
	return home
}

func assertNoTelemetryFiles(t *testing.T, home string) {
	t.Helper()
	entries, err := os.ReadDir(home)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "telemetry") {
			t.Fatalf("found %s in %s with capture off", e.Name(), home)
		}
	}
}

func TestOpenOffCreatesNoTelemetryFiles(t *testing.T) {
	home := homeDir(t)
	for _, env := range []string{"", "bodies"} {
		t.Setenv("LEANKG_TELEMETRY", env)
		rec, err := Open(home)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if rec.Level() != Off {
			t.Fatalf("Level = %q, want off", rec.Level())
		}
		rec.RecordCall(CallEvent{Tool: "query", Transport: TransportStdio})
		rec.RecordMemory(MemoryEvent{Verb: "recall"})
		if err := rec.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		assertNoTelemetryFiles(t, home)
	}
}

func TestOpenWithConsentCreatesLedgerAndRecords(t *testing.T) {
	home := homeDir(t)
	t.Setenv("LEANKG_TELEMETRY", "")
	cfg := Config{Capture: Metadata, RetentionDays: 30, MaxBodyBytes: 1024,
		Consent: Consent{GrantedAt: time.Now(), GrantedBy: "cli", Version: ConsentVersion}}
	if err := SaveConfig(home, cfg); err != nil {
		t.Fatal(err)
	}
	rec, err := Open(home)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if rec.Level() != Metadata {
		t.Fatalf("Level = %q, want metadata", rec.Level())
	}
	rec.RecordCall(CallEvent{Transport: TransportStdio, Tool: "status", Identity: Identity{ClientName: "pi"}})
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := OpenStore(home, true)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer st.Close()
	stats, err := st.Stats(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Calls != 1 {
		t.Fatalf("calls = %d, want 1", stats.Calls)
	}
}

func TestOpenStoreReadOnlyNeverCreates(t *testing.T) {
	home := homeDir(t)
	_, err := OpenStore(home, true)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("OpenStore read-only err = %v, want ErrNotExist", err)
	}
	if _, statErr := os.Stat(home); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("read-only open created %s", home)
	}
}
