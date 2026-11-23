package index

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractWorkManagerWorker(t *testing.T) {
	src := `
            class SyncWorker(
                context: Context,
                params: WorkerParameters
            ) : Worker(context, params) {
                override fun doWork(): Result {
                    return Result.success()
                }
            }
        `
	els, _ := ExtractWorkManager("./worker/SyncWorker.kt", []byte(src))
	workers := filterElems(els, "workmanager_worker")
	if len(workers) != 1 {
		t.Fatalf("workers = %d, want 1", len(workers))
	}
	if workers[0].Name != "SyncWorker" {
		t.Fatalf("worker name = %q", workers[0].Name)
	}
}

func TestExtractWorkManagerCoroutineWorker(t *testing.T) {
	src := `
            class DataFetchWorker(
                context: Context,
                params: WorkerParameters
            ) : CoroutineWorker(context, params) {
                override suspend fun doWork(): Result {
                    return Result.success()
                }
            }
        `
	els, _ := ExtractWorkManager("./worker/DataFetchWorker.kt", []byte(src))
	workers := filterElems(els, "workmanager_coroutine_worker")
	if len(workers) != 1 {
		t.Fatalf("coroutine workers = %d, want 1", len(workers))
	}
	if workers[0].Name != "DataFetchWorker" {
		t.Fatalf("worker name = %q", workers[0].Name)
	}
}

func TestExtractWorkManagerRequests(t *testing.T) {
	oneTime := `
            val request = OneTimeWorkRequestBuilder<SyncWorker>()
                .build()
        `
	_, rels := ExtractWorkManager("./app/AppModule.kt", []byte(oneTime))
	rel := findRel(rels, "workmanager_works_on")
	if rel == nil || rel.Target != "./app/AppModule.kt::WorkManager:SyncWorker" {
		t.Fatalf("one-time rel = %v", rel)
	}
	if rel.Metadata["request_type"] != "OneTimeWorkRequest" {
		t.Fatalf("request_type = %v", rel.Metadata["request_type"])
	}

	periodic := `
            val request = PeriodicWorkRequestBuilder<RefreshWorker>(1, TimeUnit.HOURS)
                .build()
        `
	_, rels = ExtractWorkManager("./app/AppModule.kt", []byte(periodic))
	rel = findRel(rels, "workmanager_works_on")
	if rel == nil || rel.Target != "./app/AppModule.kt::WorkManager:RefreshWorker" {
		t.Fatalf("periodic rel = %v", rel)
	}
	if rel.Metadata["request_type"] != "PeriodicWorkRequest" {
		t.Fatalf("request_type = %v", rel.Metadata["request_type"])
	}
}

func TestExtractWorkManagerFixture(t *testing.T) {
	els, rels := ExtractWorkManager("ui/MainActivity.kt", mustReadFixture(t, "MainActivity.kt"))
	if got := countType(els, "workmanager_worker"); got != 1 {
		t.Fatalf("workers = %d, want 1", got)
	}
	if got := countType(els, "workmanager_coroutine_worker"); got != 1 {
		t.Fatalf("coroutine workers = %d, want 1", got)
	}
	if got := countRels(rels, "workmanager_works_on"); got != 2 {
		t.Fatalf("workmanager_works_on = %d, want 2", got)
	}
}

// TestWorkManagerRequestEdgeSurvivesOrphanSweep: the request edge's source is
// its file, which has no element. The orphan sweep must keep it while the file
// exists (also on a run that changes another file) and drop it once the file
// is deleted.
func TestWorkManagerRequestEdgeSurvivesOrphanSweep(t *testing.T) {
	const sync = "class SyncWorker(context: Context, params: WorkerParameters) : Worker(context, params) {\n" +
		"    override fun doWork(): Result = Result.success()\n}\n\n" +
		"val request = OneTimeWorkRequestBuilder<SyncWorker>().build()\n"
	const edge, from = "workmanager_works_on", "Sync.kt"
	const target = "Sync.kt::WorkManager:SyncWorker"
	st, dir := newCrossTree(t)
	putFiles(t, dir, map[string]string{from: sync})
	indexTree(t, st, dir)
	if _, ok := edgeTo(t, st, from, target, edge); !ok {
		t.Fatal("request edge of a fresh index was not stored")
	}

	putFiles(t, dir, map[string]string{"Other.kt": "package app\n\nfun Other() {}\n"})
	indexTree(t, st, dir)
	if _, ok := edgeTo(t, st, from, target, edge); !ok {
		t.Fatal("the orphan sweep of a run that changed another file dropped the request edge")
	}

	if err := os.Remove(filepath.Join(dir, from)); err != nil {
		t.Fatal(err)
	}
	indexTree(t, st, dir)
	if _, ok := edgeTo(t, st, from, target, edge); ok {
		t.Fatal("request edge of a deleted file survived the sweep")
	}
}
