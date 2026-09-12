package index

import (
	"regexp"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Ports of src/indexer/gradle_extractor.rs and
// src/indexer/gradle_module_extractor.rs (Rust reference f7624143^).

var (
	gradleDepScopes = []string{
		"implementation", "api", "compileOnly", "runtimeOnly",
		"testImplementation", "testCompileOnly", "testRuntimeOnly",
	}
)

// gradleParenContent returns the text between the first "(" after prefix and
// the next ")".
func gradleParenContent(line, prefix string) (string, bool) {
	start := strings.Index(line, prefix+"(")
	if start < 0 {
		return "", false
	}
	rest := line[start+len(prefix)+1:]
	end := strings.IndexByte(rest, ')')
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}

// gradleStringAssignment matches `key = "value"` / `key=value` line starts.
func gradleStringAssignment(line, key string) (string, bool) {
	if !strings.HasPrefix(line, key+" =") && !strings.HasPrefix(line, key+"=") {
		return "", false
	}
	parts := strings.SplitN(line, "=", 2)
	if len(parts) < 2 {
		return "", false
	}
	cleaned := strings.TrimSpace(strings.Trim(strings.TrimSpace(parts[1]), `"`))
	return cleaned, cleaned != ""
}

// gradleDependencyScope classifies the configuration of a dependency line
// (parity with the Rust extract_dependency_scope, including the quirk that
// testRuntimeOnly falls through to "main").
func gradleDependencyScope(line string) string {
	switch {
	case strings.Contains(line, "testImplementation"), strings.Contains(line, "testCompileOnly"):
		return "test"
	case strings.Contains(line, "compileOnly"):
		return "compileOnly"
	case strings.Contains(line, "runtimeOnly"):
		return "runtime"
	default:
		return "main"
	}
}

// gradlePluginID extracts a plugin id from `id("x")` or `kotlin("jvm")`.
func gradlePluginID(line string) (string, bool) {
	if inner, ok := gradleParenContent(line, "id"); ok {
		return strings.Trim(inner, `"`), true
	}
	if inner, ok := gradleParenContent(line, "kotlin"); ok {
		return "kotlin-" + strings.Trim(inner, `"`), true
	}
	return "", false
}

// ExtractGradle extracts build-file elements, dependencies and plugins from a
// build.gradle / build.gradle.kts / settings.gradle(.kts) file.
func ExtractGradle(filePath string, src []byte) ([]store.Element, []store.Relationship) {
	content := string(src)
	var elements []store.Element
	var relationships []store.Relationship

	fileName := baseOf(filePath)
	elements = append(elements, store.Element{
		QualifiedName: filePath,
		ElementType:   "build_file",
		Name:          fileName,
		FilePath:      filePath,
		Language:      "gradle",
	})

	group, version, artifactID := "", "", ""
	parent := parentDirName(filePath)

	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)

		if g, ok := gradleStringAssignment(line, "group"); ok {
			group = g
		}
		if v, ok := gradleStringAssignment(line, "version"); ok {
			version = v
		}
		if a, ok := gradleStringAssignment(line, "artifactId"); ok {
			artifactID = a
		}

		for _, scope := range gradleDepScopes {
			if !strings.Contains(line, scope+"(") {
				continue
			}
			inner, ok := gradleParenContent(line, scope)
			if !ok {
				continue
			}
			parts := strings.Split(inner, ":")
			dep := inner
			if len(parts) >= 2 {
				dep = strings.Join(parts[:2], ":")
			}
			depID := "__dep__" + dep
			elements = append(elements, store.Element{
				QualifiedName: depID,
				ElementType:   "dependency",
				Name:          dep,
				FilePath:      filePath,
				Language:      "gradle",
			})
			relationships = append(relationships, store.Relationship{
				Source: filePath, Target: depID,
				RelType: "has_dependency", Confidence: 1.0,
				Metadata: map[string]any{"scope": gradleDependencyScope(line)},
			})
		}

		if plugin, ok := gradlePluginID(line); ok {
			pluginID := "__plugin__" + plugin
			elements = append(elements, store.Element{
				QualifiedName: pluginID,
				ElementType:   "plugin",
				Name:          plugin,
				FilePath:      filePath,
				Language:      "gradle",
			})
			relationships = append(relationships, store.Relationship{
				Source: filePath, Target: pluginID,
				RelType: "uses_plugin", Confidence: 1.0,
			})
		}
	}

	projectName := group
	if projectName == "" {
		projectName = artifactID
	}
	if projectName == "" {
		projectName = parent
	}
	elements = append(elements, store.Element{
		QualifiedName: "__gradle_project__" + projectName,
		ElementType:   "gradle_project",
		Name:          projectName,
		FilePath:      filePath,
		Language:      "gradle",
		Metadata: map[string]any{
			"group":       nilIfEmpty(group),
			"version":     nilIfEmpty(version),
			"artifact_id": nilIfEmpty(artifactID),
		},
	})

	return elements, relationships
}

var (
	gradleProjectDepRe = regexp.MustCompile(`(?:implementation|api|compileOnly|runtimeOnly|testImplementation)\s*\(\s*project\s*\(\s*"([^"]+)"\s*\)\s*\)`)
	gradleCatalogRe    = regexp.MustCompile(`(?:implementation|api|compileOnly|runtimeOnly|testImplementation)\s*\(\s*libs\.([\w.]+)\s*\)`)
	gradleExternalRe   = regexp.MustCompile(`(?:implementation|api|compileOnly|runtimeOnly|testImplementation)\s*\(\s*"([^"]+:[^"]+:[^"]+)"\s*\)`)
)

// ExtractGradleModuleRels extracts module-level dependency relationships
// (project(":..."), libs.* catalog refs, "group:name:version" externals).
// Returns no elements (parity with GradleModuleExtractor).
func ExtractGradleModuleRels(filePath string, src []byte) []store.Relationship {
	content := string(src)
	var relationships []store.Relationship

	for _, m := range gradleProjectDepRe.FindAllStringSubmatch(content, -1) {
		modulePath := m[1]
		moduleName := strings.TrimLeft(modulePath, ":")
		relationships = append(relationships, store.Relationship{
			Source: filePath, Target: "module:" + moduleName,
			RelType: "depends_on_module", Confidence: 0.95,
			Metadata: map[string]any{
				"module_path":     modulePath,
				"module_name":     moduleName,
				"dependency_type": "project",
			},
		})
	}

	for _, m := range gradleCatalogRe.FindAllStringSubmatch(content, -1) {
		libRef := m[1]
		relationships = append(relationships, store.Relationship{
			Source: filePath, Target: "catalog:libs." + libRef,
			RelType: "uses_library", Confidence: 0.90,
			Metadata: map[string]any{
				"catalog_ref": "libs." + libRef,
				"source":      "version_catalog",
			},
		})
	}

	for _, m := range gradleExternalRe.FindAllStringSubmatch(content, -1) {
		depString := m[1]
		parts := strings.Split(depString, ":")
		if len(parts) != 3 {
			continue
		}
		group, name, version := parts[0], parts[1], parts[2]
		relationships = append(relationships, store.Relationship{
			Source: filePath, Target: "lib:" + group + ":" + name,
			RelType: "uses_library", Confidence: 0.95,
			Metadata: map[string]any{
				"group":      group,
				"name":       name,
				"version":    version,
				"full_coord": depString,
				"source":     "external",
			},
		})
	}

	return relationships
}

// parentDirName returns the last path segment of the parent directory
// ("unknown" when there is none) — parity with the Rust parent fallback.
func parentDirName(filePath string) string {
	idx := strings.LastIndexByte(filePath, '/')
	if idx <= 0 {
		return "unknown"
	}
	rest := filePath[:idx]
	if p := strings.LastIndexByte(rest, '/'); p >= 0 {
		return rest[p+1:]
	}
	return rest
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
