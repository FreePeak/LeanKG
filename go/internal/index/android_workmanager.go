package index

import (
	"regexp"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Port of src/indexer/android_workmanager.rs (Rust reference f7624143^):
// WorkManager worker classes and work-request usages.

var (
	wmWorkerRe    = regexp.MustCompile(`(?s)(?:abstract\s+)?class\s+(\w+)\s*\(.*?\)\s*:\s*(?:androidx\.work\.)?(?:Worker|ListenableWorker)`)
	wmCoroutineRe = regexp.MustCompile(`(?s)(?:abstract\s+)?class\s+(\w+)\s*\(.*?\)\s*:\s*(?:androidx\.work\.)?CoroutineWorker`)
	wmListenerRe  = regexp.MustCompile(`(?s)(?:abstract\s+)?class\s+(\w+)\s*:\s*(?:androidx\.work\.ListenerWorker|ListenerWorker)`)
	wmOneTimeRe   = regexp.MustCompile(`OneTimeWorkRequest(?:Builder)?\s*<(\w+)>`)
	wmPeriodicRe  = regexp.MustCompile(`PeriodicWorkRequest(?:Builder)?\s*<(\w+)>`)
)

// ExtractWorkManager extracts WorkManager workers and work-request
// relationships from Kotlin source.
func ExtractWorkManager(filePath string, src []byte) ([]store.Element, []store.Relationship) {
	content := string(src)
	var elements []store.Element
	var relationships []store.Relationship

	worker := func(name, elemType, workerType string) store.Element {
		return store.Element{
			QualifiedName: filePath + "::WorkManager:" + name,
			ElementType:   elemType,
			Name:          name,
			FilePath:      filePath,
			Language:      "kotlin",
			Metadata:      map[string]any{"worker_class": name, "worker_type": workerType},
		}
	}

	for _, m := range wmWorkerRe.FindAllStringSubmatch(content, -1) {
		elements = append(elements, worker(m[1], "workmanager_worker", "Worker"))
	}
	for _, m := range wmCoroutineRe.FindAllStringSubmatch(content, -1) {
		elements = append(elements, worker(m[1], "workmanager_coroutine_worker", "CoroutineWorker"))
	}
	for _, m := range wmListenerRe.FindAllStringSubmatch(content, -1) {
		elements = append(elements, worker(m[1], "workmanager_listener_worker", "ListenerWorker"))
	}

	for _, m := range wmOneTimeRe.FindAllStringSubmatch(content, -1) {
		relationships = append(relationships, store.Relationship{
			Source: filePath, Target: filePath + "::WorkManager:" + m[1],
			RelType: "workmanager_works_on", Confidence: 0.85,
			Metadata: map[string]any{
				"request_type": "OneTimeWorkRequest",
				"worker_class": m[1],
			},
		})
	}
	for _, m := range wmPeriodicRe.FindAllStringSubmatch(content, -1) {
		relationships = append(relationships, store.Relationship{
			Source: filePath, Target: filePath + "::WorkManager:" + m[1],
			RelType: "workmanager_works_on", Confidence: 0.85,
			Metadata: map[string]any{
				"request_type": "PeriodicWorkRequest",
				"worker_class": m[1],
			},
		})
	}

	return elements, relationships
}
