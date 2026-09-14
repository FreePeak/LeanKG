package index

import (
	"testing"
)

func TestSpecialistClaim(t *testing.T) {
	claimed := []string{
		"AndroidManifest.xml",
		"app/AndroidManifest.xml",
		"app/src/main/res/values/strings.xml",
		"app/src/main/res/navigation/nav_graph.xml",
		"build.gradle",
		"build.gradle.kts",
		"settings.gradle",
		"settings.gradle.kts",
		"pom.xml",
	}
	for _, rel := range claimed {
		if !SpecialistClaim(rel) {
			t.Fatalf("SpecialistClaim(%q) = false, want true", rel)
		}
	}

	unclaimed := []string{
		"app/src/main/res/layout/activity_main.xml", // XmlLayoutExtractor: out of scope
		"strings.xml",
		"androidmanifest.xml",
		"gradle.properties",
		"build.gradle.kts.bak",
		"main.go",
		"src/App.kt",
	}
	for _, rel := range unclaimed {
		if SpecialistClaim(rel) {
			t.Fatalf("SpecialistClaim(%q) = true, want false", rel)
		}
	}
}

func TestExtractSpecialistDispatch(t *testing.T) {
	els, rels, claimed := ExtractSpecialist("AndroidManifest.xml", mustReadFixture(t, "AndroidManifest.xml"))
	if !claimed || len(els) == 0 || len(rels) == 0 {
		t.Fatalf("manifest dispatch: claimed=%v els=%d rels=%d", claimed, len(els), len(rels))
	}

	els, rels, claimed = ExtractSpecialist("app/src/main/res/values/strings.xml", mustReadFixture(t, "strings.xml"))
	if !claimed || countType(els, "android_string") != 2 || countRels(rels, "defines_string") != 2 {
		t.Fatalf("resources dispatch: claimed=%v els=%d rels=%d", claimed, len(els), len(rels))
	}

	els, rels, claimed = ExtractSpecialist("app/src/main/res/navigation/nav_graph.xml", mustReadFixture(t, "nav_graph.xml"))
	if !claimed || countType(els, "nav_destination") != 3 {
		t.Fatalf("navigation dispatch: claimed=%v els=%d", claimed, len(els))
	}
	if countRels(rels, "nav_action") != 1 {
		t.Fatalf("navigation dispatch nav_action = %d", countRels(rels, "nav_action"))
	}

	els, rels, claimed = ExtractSpecialist("app/build.gradle.kts", mustReadFixture(t, "build.gradle.kts"))
	if !claimed || countType(els, "build_file") != 1 || countRels(rels, "depends_on_module") != 2 {
		t.Fatalf("gradle dispatch: claimed=%v els=%d rels=%d", claimed, len(els), len(rels))
	}

	els, rels, claimed = ExtractSpecialist("service/pom.xml", mustReadFixture(t, "pom.xml"))
	if !claimed || countType(els, "maven_project") != 1 || countRels(rels, "has_dependency") != 2 {
		t.Fatalf("maven dispatch: claimed=%v els=%d rels=%d", claimed, len(els), len(rels))
	}

	// Unclaimed path returns claimed=false and no data.
	if _, _, claimed = ExtractSpecialist("main.go", []byte("package main")); claimed {
		t.Fatal("main.go should not be claimed")
	}
	if _, _, claimed = ExtractSpecialist("app/src/main/res/layout/activity_main.xml", []byte("<merge/>")); claimed {
		t.Fatal("layout XML is out of scope and must not be claimed")
	}
}

func TestKotlinExtrasAggregation(t *testing.T) {
	els, rels := KotlinExtras("ui/MainActivity.kt", mustReadFixture(t, "MainActivity.kt"))

	// Room: none in this fixture.
	if got := countType(els, "room_entity"); got != 0 {
		t.Fatalf("room entities = %d, want 0", got)
	}
	// Hilt.
	if got := countType(els, "hilt_module"); got != 1 {
		t.Fatalf("hilt modules = %d, want 1", got)
	}
	if got := countType(els, "hilt_provider"); got != 1 {
		t.Fatalf("hilt providers = %d, want 1", got)
	}
	// WorkManager.
	if got := countType(els, "workmanager_worker"); got != 1 {
		t.Fatalf("workmanager workers = %d, want 1", got)
	}
	if got := countType(els, "workmanager_coroutine_worker"); got != 1 {
		t.Fatalf("workmanager coroutine workers = %d, want 1", got)
	}
	// Relationships from every Kotlin specialist.
	for _, typ := range []string{
		"hilt_module_provides", "hilt_provides", "hilt_injected", "hilt_field_injected",
		"uses_string_resource", "uses_drawable_resource", "references_view_by_id",
		"uses_viewbinding", "on_click_handler", "navigates_to", "workmanager_works_on",
	} {
		if countRels(rels, typ) == 0 {
			t.Fatalf("missing relationship type %s", typ)
		}
	}
	// Compose DSL markers are absent: no nav_graph element.
	if countType(els, "nav_graph") != 0 {
		t.Fatalf("DSL extractor ran without markers")
	}
}

func TestKotlinExtrasLeanback(t *testing.T) {
	els, rels := KotlinExtras("tv/MainFragment.kt", mustReadFixture(t, "MainFragment.kt"))
	if got := countType(els, "nav_destination"); got != 1 {
		t.Fatalf("leanback destinations = %d, want 1", got)
	}
	if got := countRels(rels, "presents"); got == 0 {
		t.Fatal("missing presents relationships")
	}
}

func TestKotlinExtrasComposeDSL(t *testing.T) {
	els, rels := KotlinExtras("nav/NavHost.kt", mustReadFixture(t, "NavHost.kt"))
	if got := countType(els, "nav_graph"); got != 1 {
		t.Fatalf("nav_graph = %d, want 1", got)
	}
	if got := countType(els, "nav_destination"); got != 4 {
		t.Fatalf("destinations = %d, want 4", got)
	}
	if got := countRels(rels, "nav_action"); got != 1 {
		t.Fatalf("nav_action = %d, want 1", got)
	}
}

func TestKotlinExtrasRoomFile(t *testing.T) {
	els, rels := KotlinExtras("data/AppDatabase.kt", mustReadFixture(t, "AppDatabase.kt"))
	if got := countType(els, "room_entity"); got != 2 {
		t.Fatalf("room entities = %d, want 2", got)
	}
	if got := countType(els, "room_database"); got != 1 {
		t.Fatalf("room databases = %d, want 1", got)
	}
	// Annotation-style foreign keys are invisible to the body scan (parity).
	if got := countRels(rels, "room_entity_has_foreign_key"); got != 0 {
		t.Fatalf("foreign keys = %d, want 0 (annotation precedes class body)", got)
	}
}
