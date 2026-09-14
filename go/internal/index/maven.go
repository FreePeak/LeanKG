package index

import (
	"regexp"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Port of src/indexer/maven_extractor.rs (Rust reference f7624143^):
// pom.xml project coordinates and <dependency> entries.

var mavenDepRe = regexp.MustCompile(`(?s)<dependency>([\s\S]*?)</dependency>`)

// mavenExtractTag returns the first <tag>text</tag> content, trimmed.
func mavenExtractTag(content, tag string) (string, bool) {
	re := regexp.MustCompile(`<` + tag + `>([^<]+)</` + tag + `>`)
	m := re.FindStringSubmatch(content)
	if m == nil {
		return "", false
	}
	return strings.TrimSpace(m[1]), true
}

// ExtractMaven extracts the project element and dependencies from pom.xml.
func ExtractMaven(filePath string, src []byte) ([]store.Element, []store.Relationship) {
	content := string(src)
	var elements []store.Element
	var relationships []store.Relationship

	groupID, _ := mavenExtractTag(content, "groupId")
	artifactID, _ := mavenExtractTag(content, "artifactId")
	version, _ := mavenExtractTag(content, "version")
	packaging, ok := mavenExtractTag(content, "packaging")
	if !ok {
		packaging = "jar"
	}

	elements = append(elements, store.Element{
		QualifiedName: "__maven_project__" + groupID + ":" + artifactID,
		ElementType:   "maven_project",
		Name:          artifactID,
		FilePath:      filePath,
		Language:      "maven",
		Metadata: map[string]any{
			"groupId":    groupID,
			"artifactId": artifactID,
			"version":    version,
			"packaging":  packaging,
		},
	})
	for _, m := range mavenDepRe.FindAllStringSubmatch(content, -1) {
		block := m[1]
		depGroup, _ := mavenExtractTag(block, "groupId")
		depArtifact, _ := mavenExtractTag(block, "artifactId")
		depVersion, _ := mavenExtractTag(block, "version")
		depScope, hasScope := mavenExtractTag(block, "scope")
		if !hasScope {
			depScope = "compile"
		}

		if depArtifact == "" {
			continue
		}
		depID := "__dep__" + depGroup + ":" + depArtifact
		elements = append(elements, store.Element{
			QualifiedName: depID,
			ElementType:   "dependency",
			Name:          depArtifact,
			FilePath:      filePath,
			Language:      "maven",
		})
		relationships = append(relationships, store.Relationship{
			Source: filePath, Target: depID,
			RelType: "has_dependency", Confidence: 1.0,
			Metadata: map[string]any{
				"scope":   depScope,
				"version": nilIfEmpty(depVersion),
			},
		})
	}

	return elements, relationships
}
