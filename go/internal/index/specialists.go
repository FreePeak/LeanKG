package index

import (
	"bytes"
	"os"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Specialist routing: which files bypass the generic language extraction and
// go to the Android/Gradle/Maven extractors, mirroring the Rust dispatcher in
// src/indexer/mod.rs (try_extract_android + the build-file branches).

// SpecialistClaim reports whether a repo-relative file is claimed by a
// specialist extractor. Claimed files are indexed with ExtractSpecialist
// instead of the generic language pipeline.
func SpecialistClaim(rel string) bool {
	name := baseOf(rel)
	switch name {
	case "AndroidManifest.xml", "pom.xml",
		"build.gradle", "build.gradle.kts",
		"settings.gradle", "settings.gradle.kts":
		return true
	}
	if strings.HasSuffix(rel, ".xml") {
		return strings.Contains(rel, "/res/values/") || strings.Contains(rel, "/res/navigation/")
	}
	return false
}

// ExtractSpecialist extracts one claimed file. claimed is false when rel is
// not a specialist file (callers then fall back to generic extraction).
func ExtractSpecialist(rel string, src []byte) (els []store.Element, rels []store.Relationship, claimed bool) {
	name := baseOf(rel)
	switch {
	case name == "AndroidManifest.xml":
		els, rels = ExtractAndroidManifest(rel, src)
	case strings.HasSuffix(rel, ".xml") && strings.Contains(rel, "/res/values/"):
		els, rels = ExtractAndroidResources(rel, src)
	case strings.HasSuffix(rel, ".xml") && strings.Contains(rel, "/res/navigation/"):
		els, rels = ExtractJetpackNavXML(rel, src)
	case name == "build.gradle" || name == "build.gradle.kts" ||
		name == "settings.gradle" || name == "settings.gradle.kts":
		els, rels = ExtractGradle(rel, src)
		// Module-level dependency relationships ride along (Rust mod.rs
		// combines GradleExtractor + GradleModuleExtractor output).
		rels = append(rels, ExtractGradleModuleRels(rel, src)...)
	case name == "pom.xml":
		els, rels = ExtractMaven(rel, src)
	default:
		return nil, nil, false
	}
	return els, rels, true
}

// KotlinExtras runs the Kotlin-side Android extractors over one .kt/.kts file
// and returns their elements and relationships, in the same order the Rust
// dispatcher (src/indexer/mod.rs extract_elements_for_file) appends them:
// Room, Hilt, resource refs, resource links, fragment nav, leanback nav,
// Compose nav DSL (guarded), WorkManager.
func KotlinExtras(rel string, src []byte) ([]store.Element, []store.Relationship) {
	var els []store.Element
	var rels []store.Relationship

	if e, r := ExtractRoom(rel, src); true {
		els = append(els, e...)
		rels = append(rels, r...)
	}
	if e, r := ExtractHilt(rel, src); true {
		els = append(els, e...)
		rels = append(rels, r...)
	}
	rels = append(rels, ExtractResourceRefs(rel, src)...)
	rels = append(rels, ExtractResourceLinks(rel, src)...)
	rels = append(rels, ExtractFragmentNav(rel, src)...)

	if e, r := ExtractLeanbackNav(rel, src); true {
		els = append(els, e...)
		rels = append(rels, r...)
	}

	// Compose Navigation DSL: only when the markers the Rust dispatcher
	// checks for are present.
	if bytes.Contains(src, []byte("NavGraphBuilder")) || bytes.Contains(src, []byte("composable(")) {
		if e, r := ExtractJetpackNavKotlinDSL(rel, src); true {
			els = append(els, e...)
			rels = append(rels, r...)
		}
	}

	if e, r := ExtractWorkManager(rel, src); true {
		els = append(els, e...)
		rels = append(rels, r...)
	}

	return els, rels
}

// indexSpecialistFile extracts and persists one specialist-claimed file.
// Returns handled=false when the file is not claimed (caller falls back to
// generic extraction). Wiring: called from indexDir's changed-file loop
// BEFORE extractFileAs.
func indexSpecialistFile(st store.Backend, rel, abs string, size, mtimeNS int64, hash string, res *Result) (bool, error) {
	src, err := os.ReadFile(abs)
	if err != nil {
		return false, err
	}
	els, rels, claimed := ExtractSpecialist(rel, src)
	if !claimed {
		return false, nil
	}
	if err := st.DeleteByFile(rel); err != nil {
		return true, err
	}
	if err := st.UpsertElements(els); err != nil {
		return true, err
	}
	if err := st.UpsertRelationships(rels); err != nil {
		return true, err
	}
	if err := st.UpsertFiles([]store.FileRecord{{
		Path: rel, Size: size, MtimeNS: mtimeNS, ContentHash: hash,
	}}); err != nil {
		return true, err
	}
	res.Files++
	res.Elements += len(els)
	res.Relationships += len(rels)
	return true, nil
}

// indexKotlinExtras persists the Kotlin-side Android extractor output for one
// already-indexed .kt/.kts file. Wiring: called from indexDir after the
// generic upsert for files with lang == "kotlin".
func indexKotlinExtras(st store.Backend, rel, abs string, res *Result) error {
	src, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	els, rels := KotlinExtras(rel, src)
	if err := st.UpsertElements(els); err != nil {
		return err
	}
	if err := st.UpsertRelationships(rels); err != nil {
		return err
	}
	res.Elements += len(els)
	res.Relationships += len(rels)
	return nil
}
