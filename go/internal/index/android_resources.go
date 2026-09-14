package index

import (
	"regexp"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Port of src/indexer/android_resources.rs (Rust reference f7624143^).
// Extracts named resource definitions (<string>, <color>, <dimen>, <style>,
// <theme>, <bool>, <integer>, arrays) from res/values/*.xml files.

var (
	resStringRe  = regexp.MustCompile(`<string\s+name\s*=\s*"([^"]+)"[^>]*>([^<]*)</string>`)
	resColorRe   = regexp.MustCompile(`<color\s+name\s*=\s*"([^"]+)"[^>]*>([^<]*)</color>`)
	resDimenRe   = regexp.MustCompile(`<dimen\s+name\s*=\s*"([^"]+)"[^>]*>([^<]*)</dimen>`)
	resStyleRe   = regexp.MustCompile(`<style\s+name\s*=\s*"([^"]+)"(?:\s+parent\s*=\s*"([^"]*)")?[^>]*>[\s\S]*?</style>`)
	resThemeRe   = regexp.MustCompile(`<theme\s+name\s*=\s*"([^"]+)"[^>]*>[\s\S]*?</theme>`)
	resBoolRe    = regexp.MustCompile(`<bool\s+name\s*=\s*"([^"]+)"[^>]*>([^<]*)</bool>`)
	resIntegerRe = regexp.MustCompile(`<integer\s+name\s*=\s*"([^"]+)"[^>]*>([^<]*)</integer>`)
	resArrayRe   = regexp.MustCompile(`<(?:string-array|integer-array|plurals)\s+name\s*=\s*"([^"]+)"[^>]*>[\s\S]*?</(?:string-array|integer-array|plurals)>`)
)

// ExtractAndroidResources extracts resource definitions from a res/values XML
// file, plus one file-level element typed by the file name.
func ExtractAndroidResources(filePath string, src []byte) ([]store.Element, []store.Relationship) {
	content := string(src)
	var elements []store.Element
	var relationships []store.Relationship

	fileName := baseOf(filePath)
	elements = append(elements, store.Element{
		QualifiedName: filePath,
		ElementType:   detectResourceType(fileName),
		Name:          fileName,
		FilePath:      filePath,
		Language:      "android",
	})

	for _, m := range resStringRe.FindAllStringSubmatch(content, -1) {
		qn := filePath + "/@string/" + m[1]
		elements = append(elements, store.Element{
			QualifiedName: qn, ElementType: "android_string", Name: m[1],
			FilePath: filePath, Language: "android",
			Metadata: map[string]any{"value": strings.TrimSpace(m[2]), "resource_type": "string"},
		})
		relationships = append(relationships, store.Relationship{
			Source: filePath, Target: qn, RelType: "defines_string", Confidence: 1.0,
			Metadata: map[string]any{"value": strings.TrimSpace(m[2])},
		})
	}

	for _, m := range resColorRe.FindAllStringSubmatch(content, -1) {
		qn := filePath + "/@color/" + m[1]
		elements = append(elements, store.Element{
			QualifiedName: qn, ElementType: "android_color", Name: m[1],
			FilePath: filePath, Language: "android",
			Metadata: map[string]any{"value": strings.TrimSpace(m[2]), "resource_type": "color"},
		})
		relationships = append(relationships, store.Relationship{
			Source: filePath, Target: qn, RelType: "defines_color", Confidence: 1.0,
			Metadata: map[string]any{"value": strings.TrimSpace(m[2])},
		})
	}

	for _, m := range resDimenRe.FindAllStringSubmatch(content, -1) {
		qn := filePath + "/@dimen/" + m[1]
		elements = append(elements, store.Element{
			QualifiedName: qn, ElementType: "android_dimen", Name: m[1],
			FilePath: filePath, Language: "android",
			Metadata: map[string]any{"value": strings.TrimSpace(m[2]), "resource_type": "dimen"},
		})
		relationships = append(relationships, store.Relationship{
			Source: filePath, Target: qn, RelType: "defines_dimen", Confidence: 1.0,
			Metadata: map[string]any{"value": strings.TrimSpace(m[2])},
		})
	}

	for _, m := range resStyleRe.FindAllStringSubmatch(content, -1) {
		qn := filePath + "/@style/" + m[1]
		parent := optionalCapture(m, 2)
		elements = append(elements, store.Element{
			QualifiedName: qn, ElementType: "android_style", Name: m[1],
			FilePath: filePath, Language: "android",
			Metadata: map[string]any{"parent": parent, "resource_type": "style"},
		})
		relationships = append(relationships, store.Relationship{
			Source: filePath, Target: qn, RelType: "defines_style", Confidence: 1.0,
		})
		if parent != nil && parent != "" {
			relationships = append(relationships, store.Relationship{
				Source: qn, Target: "@style/" + parent.(string),
				RelType: "inherits_from", Confidence: 1.0,
				Metadata: map[string]any{"parent": parent},
			})
		}
	}

	for _, m := range resThemeRe.FindAllStringSubmatch(content, -1) {
		qn := filePath + "/@theme/" + m[1]
		elements = append(elements, store.Element{
			QualifiedName: qn, ElementType: "android_theme", Name: m[1],
			FilePath: filePath, Language: "android",
			Metadata: map[string]any{"resource_type": "theme"},
		})
		relationships = append(relationships, store.Relationship{
			Source: filePath, Target: qn, RelType: "defines_theme", Confidence: 1.0,
		})
	}

	for _, m := range resBoolRe.FindAllStringSubmatch(content, -1) {
		qn := filePath + "/@bool/" + m[1]
		elements = append(elements, store.Element{
			QualifiedName: qn, ElementType: "android_bool", Name: m[1],
			FilePath: filePath, Language: "android",
			Metadata: map[string]any{"value": strings.TrimSpace(m[2]), "resource_type": "bool"},
		})
		relationships = append(relationships, store.Relationship{
			Source: filePath, Target: qn, RelType: "defines_bool", Confidence: 1.0,
			Metadata: map[string]any{"value": strings.TrimSpace(m[2])},
		})
	}

	for _, m := range resIntegerRe.FindAllStringSubmatch(content, -1) {
		qn := filePath + "/@integer/" + m[1]
		elements = append(elements, store.Element{
			QualifiedName: qn, ElementType: "android_integer", Name: m[1],
			FilePath: filePath, Language: "android",
			Metadata: map[string]any{"value": strings.TrimSpace(m[2]), "resource_type": "integer"},
		})
		relationships = append(relationships, store.Relationship{
			Source: filePath, Target: qn, RelType: "defines_integer", Confidence: 1.0,
			Metadata: map[string]any{"value": strings.TrimSpace(m[2])},
		})
	}

	for _, m := range resArrayRe.FindAllStringSubmatch(content, -1) {
		qn := filePath + "/@array/" + m[1]
		elements = append(elements, store.Element{
			QualifiedName: qn, ElementType: "android_array", Name: m[1],
			FilePath: filePath, Language: "android",
			Metadata: map[string]any{"resource_type": "array"},
		})
		relationships = append(relationships, store.Relationship{
			Source: filePath, Target: qn, RelType: "defines_array", Confidence: 1.0,
		})
	}

	return elements, relationships
}

// detectResourceType types the file element by its base file name.
func detectResourceType(fileName string) string {
	switch {
	case strings.HasPrefix(fileName, "strings"):
		return "android_strings"
	case strings.HasPrefix(fileName, "colors"):
		return "android_colors"
	case strings.HasPrefix(fileName, "dimens"):
		return "android_dimens"
	case strings.HasPrefix(fileName, "styles"):
		return "android_styles"
	case strings.HasPrefix(fileName, "bools"), strings.HasPrefix(fileName, "bool"):
		return "android_bools"
	case strings.HasPrefix(fileName, "integers"), strings.HasPrefix(fileName, "integer"):
		return "android_integers"
	case strings.HasPrefix(fileName, "arrays"), strings.HasPrefix(fileName, "plurals"):
		return "android_arrays"
	default:
		return "android_values"
	}
}
