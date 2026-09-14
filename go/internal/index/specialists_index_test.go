//go:build !tstree

package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// TestIndexSpecialistFilePersists drives the index-integration helper against a
// real store and verifies the claims contract Main wires into indexDir:
// claimed files are persisted with their specialist elements/relationships and
// counted; unclaimed files are left to the generic path.
func TestIndexSpecialistFilePersists(t *testing.T) {
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join("testdata", "AndroidManifest.xml"))
	if err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(dir, "AndroidManifest.xml")
	if err := os.WriteFile(abs, src, 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}

	var res Result
	handled, err := indexSpecialistFile(st, "AndroidManifest.xml", abs, int64(len(src)), 1, "hash", &res)
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatal("AndroidManifest.xml should be handled")
	}
	if res.Files != 1 || res.Elements == 0 || res.Relationships == 0 {
		t.Fatalf("result = %+v, want file/element/relationship counts", res)
	}

	els, err := st.FindExact("__android__activity___MainActivity")
	if err != nil {
		t.Fatal(err)
	}
	if len(els) != 1 || els[0].ElementType != "android_activity" {
		t.Fatalf("persisted activity = %v", els)
	}
	rels, err := st.Outgoing("AndroidManifest.xml")
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) == 0 {
		t.Fatal("manifest relationships not persisted")
	}
	files, err := st.Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "AndroidManifest.xml" {
		t.Fatalf("files = %v", files)
	}

	// Unclaimed files report handled=false and write nothing.
	unclaimed, err := indexSpecialistFile(st, "main.go", abs, 1, 1, "hash2", &res)
	if err != nil {
		t.Fatal(err)
	}
	if unclaimed {
		t.Fatal("main.go must not be handled by the specialist path")
	}
}

// TestIndexKotlinExtrasPersists drives the Kotlin augmentation helper: a
// .kts build file is not claimed (it routes through the Gradle specialist),
// while a .kt file contributes rooms/hilt/workmanager edges on top of the
// generic extraction.
func TestIndexKotlinExtrasPersists(t *testing.T) {
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join("testdata", "MainActivity.kt"))
	if err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(dir, "MainActivity.kt")
	if err := os.WriteFile(abs, src, 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}

	var res Result
	if err := indexKotlinExtras(st, "ui/MainActivity.kt", abs, &res); err != nil {
		t.Fatal(err)
	}
	if res.Elements == 0 {
		t.Fatal("expected Kotlin extras elements")
	}
	els, err := st.FindExact("ui/MainActivity.kt::HiltModule:AppModule")
	if err != nil {
		t.Fatal(err)
	}
	if len(els) != 1 {
		t.Fatalf("hilt module not persisted: %v", els)
	}
	rels, err := st.Outgoing("ui/MainActivity.kt::WorkManager:SyncWorker")
	if err != nil {
		t.Fatal(err)
	}
	_ = rels // worker element has no outgoing edges; the file does.
	fileRels, err := st.Outgoing("ui/MainActivity.kt")
	if err != nil {
		t.Fatal(err)
	}
	if len(fileRels) == 0 {
		t.Fatal("kotlin extras relationships not persisted")
	}
}
