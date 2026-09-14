package index

import (
	"strings"
	"testing"
)

func TestExtractGradleDependencies(t *testing.T) {
	src := `
plugins {
    id("org.springframework.boot") version "3.2.0"
    kotlin("jvm") version "1.9.20"
}

dependencies {
    implementation("com.example:core:1.0.0")
    testImplementation("org.junit.jupiter:junit-jupiter:5.10.0")
}
`
	_, rels := ExtractGradle("build.gradle.kts", []byte(src))
	deps := filterRels(rels, "has_dependency")
	if len(deps) < 2 {
		t.Fatalf("has_dependency = %d, want >= 2", len(deps))
	}
	plugins := filterRels(rels, "uses_plugin")
	if len(plugins) != 2 {
		t.Fatalf("uses_plugin = %d, want 2", len(plugins))
	}
}

func TestExtractGradleGroupArtifact(t *testing.T) {
	src := `
group = "com.example"
version = "1.0.0"
`
	els, _ := ExtractGradle("build.gradle.kts", []byte(src))
	project := filterElems(els, "gradle_project")
	if len(project) == 0 {
		t.Fatal("should extract project metadata")
	}
	if project[0].Name != "com.example" {
		t.Fatalf("project name = %q, want com.example", project[0].Name)
	}
	if project[0].Metadata["version"] != "1.0.0" {
		t.Fatalf("version = %v, want 1.0.0", project[0].Metadata["version"])
	}
	if project[0].Metadata["group"] != "com.example" {
		t.Fatalf("group = %v", project[0].Metadata["group"])
	}
}

func TestExtractGradleFixture(t *testing.T) {
	els, rels := ExtractGradle("app/build.gradle.kts", mustReadFixture(t, "build.gradle.kts"))

	if !hasTypedName(els, "build_file", "build.gradle.kts") {
		t.Fatal("missing build_file element")
	}
	if got := countRels(rels, "has_dependency"); got != 6 {
		t.Fatalf("has_dependency = %d, want 6", got)
	}
	if got := countRels(rels, "uses_plugin"); got != 2 {
		t.Fatalf("uses_plugin = %d, want 2", got)
	}
	project := findElem(els, "gradle_project", "")
	if project == nil {
		t.Fatal("missing gradle_project element")
	}
	// No group/artifactId in the fixture: the parent directory name is used.
	if project.Name != "app" {
		t.Fatalf("project name = %q, want app", project.Name)
	}
	if project.Metadata["group"] != nil || project.Metadata["version"] != nil {
		t.Fatalf("expected null group/version, got %v", project.Metadata)
	}
	// testImplementation("junit:junit:4.13.2") is scoped "test".
	var testScope bool
	for _, r := range rels {
		if r.RelType == "has_dependency" && r.Metadata["scope"] == "test" &&
			strings.Contains(r.Target, "junit") {
			testScope = true
		}
	}
	if !testScope {
		t.Fatal("missing test-scoped junit dependency")
	}
}

func TestExtractGradleModuleProjectDeps(t *testing.T) {
	src := `
            dependencies {
                implementation(project(":core"))
                api(project(":feature:login"))
                testImplementation(project(":test:common"))
            }
        `
	rels := ExtractGradleModuleRels("./app/build.gradle.kts", []byte(src))
	projectDeps := filterRels(rels, "depends_on_module")
	if len(projectDeps) != 3 {
		t.Fatalf("depends_on_module = %d, want 3", len(projectDeps))
	}
	var hasCore, hasFeatureLogin bool
	for _, r := range projectDeps {
		hasCore = hasCore || r.Target == "module:core"
		hasFeatureLogin = hasFeatureLogin || r.Target == "module:feature:login"
	}
	if !hasCore || !hasFeatureLogin {
		t.Fatalf("targets = %v", projectDeps)
	}
	if projectDeps[0].Confidence != 0.95 {
		t.Fatalf("confidence = %v, want 0.95", projectDeps[0].Confidence)
	}
}

func TestExtractGradleModuleCatalogRefs(t *testing.T) {
	src := `
            dependencies {
                implementation(libs.androidx.room.runtime)
                implementation(libs.kotlinx.coroutines.android)
                api(libs.retrofit)
            }
        `
	rels := ExtractGradleModuleRels("./app/build.gradle.kts", []byte(src))
	var catalogRefs []string
	for _, r := range rels {
		if r.RelType == "uses_library" && r.Metadata["source"] == "version_catalog" {
			catalogRefs = append(catalogRefs, r.Target)
		}
	}
	if len(catalogRefs) == 0 {
		t.Fatal("should find version catalog refs")
	}
	var hasRoom bool
	for _, tgt := range catalogRefs {
		hasRoom = hasRoom || tgt == "catalog:libs.androidx.room.runtime"
	}
	if !hasRoom {
		t.Fatalf("targets = %v", catalogRefs)
	}
}

func TestExtractGradleModuleExternalDeps(t *testing.T) {
	src := `
            dependencies {
                implementation("com.squareup.retrofit2:retrofit:2.9.0")
                implementation("io.coil-kt:coil-compose:2.4.0")
            }
        `
	rels := ExtractGradleModuleRels("./app/build.gradle.kts", []byte(src))
	var external []string
	for _, r := range rels {
		if r.RelType == "uses_library" && r.Metadata["source"] == "external" {
			external = append(external, r.Target)
		}
	}
	if len(external) != 2 {
		t.Fatalf("external deps = %d, want 2", len(external))
	}
	var hasRetrofit bool
	for _, tgt := range external {
		hasRetrofit = hasRetrofit || tgt == "lib:com.squareup.retrofit2:retrofit"
	}
	if !hasRetrofit {
		t.Fatalf("targets = %v", external)
	}
}

func TestExtractGradleModuleFixture(t *testing.T) {
	rels := ExtractGradleModuleRels("app/build.gradle.kts", mustReadFixture(t, "build.gradle.kts"))
	if got := countRels(rels, "depends_on_module"); got != 2 {
		t.Fatalf("depends_on_module = %d, want 2", got)
	}
	var external, catalog int
	for _, r := range rels {
		if r.RelType != "uses_library" {
			continue
		}
		switch r.Metadata["source"] {
		case "external":
			external++
		case "version_catalog":
			catalog++
		}
	}
	if external != 2 {
		t.Fatalf("external = %d, want 2", external)
	}
	if catalog != 2 {
		t.Fatalf("catalog = %d, want 2", catalog)
	}
}
