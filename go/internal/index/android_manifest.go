package index

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Port of src/indexer/android_manifest.rs (Rust reference f7624143^).
//
// The manifest is parsed with the same attribute-content regexes the Rust
// extractor used (not a full XML parse), so the quirks — e.g. <application>
// only yields an android_application element when the tag has no attributes —
// are intentional parity behavior.

var (
	manifestNameRe       = regexp.MustCompile(`android:name\s*=\s*["']([^"']+)["']`)
	manifestActionRe     = regexp.MustCompile(`<action\s+android:name\s*=\s*["']([^"']+)["']`)
	manifestCategoryRe   = regexp.MustCompile(`<category\s+android:name\s*=\s*["']([^"']+)["']`)
	manifestMetadataRe   = regexp.MustCompile(`<meta-data\s+android:name\s*=\s*["']([^"']+)["'](?:\s+android:value\s*=\s*["']([^"']+)["'])?(?:\s+android:resource\s*=\s*["']([^"']+)["'])?`)
	manifestAppClassRe   = regexp.MustCompile(`<application[^>]*android:name\s*=\s*["']([^"']+)["']`)
	manifestAppTagRe     = regexp.MustCompile(`<application>([^<]*)</application>`)
	manifestIntentFilter = regexp.MustCompile(`(?s)<intent-filter[^>]*>(.*?)</intent-filter>`)
)

// manifestComponentTags maps manifest component tags to element types.
var manifestComponentTags = [][2]string{
	{"activity", "android_activity"},
	{"service", "android_service"},
	{"receiver", "android_broadcast_receiver"},
	{"provider", "android_content_provider"},
}

// manifestTagRe builds the per-tag component regex
// `<tag[\s>]([^>]*)>(?:[^<]*</tag>)?`.
func manifestTagRe(tag string) *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(`<%s[\s>]([^>]*)>(?:[^<]*</%s>)?`, tag, tag))
}

// ExtractAndroidManifest extracts components, permissions, features, intent
// filters and meta-data entries from an AndroidManifest.xml.
func ExtractAndroidManifest(filePath string, src []byte) ([]store.Element, []store.Relationship) {
	content := string(src)
	var elements []store.Element
	var relationships []store.Relationship

	fileName := baseOf(filePath)
	if fileName == "" {
		fileName = "AndroidManifest.xml"
	}

	elements = append(elements, store.Element{
		QualifiedName: filePath,
		ElementType:   "android_manifest",
		Name:          fileName,
		FilePath:      filePath,
		Language:      "android",
	})

	compID := func(prefix, name string) string {
		return fmt.Sprintf("__android__%s__%s", prefix, strings.Map(func(r rune) rune {
			if r == '.' || r == '$' || r == ':' || r == '-' {
				return '_'
			}
			return r
		}, name))
	}

	for _, ct := range manifestComponentTags {
		tag, elemType := ct[0], ct[1]
		re := manifestTagRe(tag)
		for _, attrs := range re.FindAllStringSubmatch(content, -1) {
			name := manifestAndroidName(attrs[1])
			if name == "" {
				continue
			}
			id := compID(tag, name)
			elements = append(elements, store.Element{
				QualifiedName: id,
				ElementType:   elemType,
				Name:          name,
				FilePath:      filePath,
				Language:      "android",
				Metadata:      map[string]any{"tag": tag},
			})
			relationships = append(relationships, store.Relationship{
				Source: filePath, Target: id,
				RelType: "declares_component", Confidence: 1.0,
			})
		}
	}

	// Application element: only for an attribute-less <application> tag with
	// inline text content (parity with the Rust extract_tag_content).
	if app := manifestAppTagRe.FindStringSubmatch(content); app != nil {
		if name := manifestAndroidName(app[1]); name != "" {
			elements = append(elements, store.Element{
				QualifiedName: compID("application", name),
				ElementType:   "android_application",
				Name:          name,
				FilePath:      filePath,
				Language:      "android",
			})
		}
	}

	for _, attrs := range manifestTagRe("uses-permission").FindAllStringSubmatch(content, -1) {
		if name := manifestAndroidName(attrs[1]); name != "" {
			id := compID("permission", name)
			elements = append(elements, store.Element{
				QualifiedName: id,
				ElementType:   "android_permission",
				Name:          name,
				FilePath:      filePath,
				Language:      "android",
			})
			relationships = append(relationships, store.Relationship{
				Source: filePath, Target: id,
				RelType: "requires_permission", Confidence: 1.0,
			})
		}
	}

	for _, attrs := range manifestTagRe("uses-feature").FindAllStringSubmatch(content, -1) {
		if name := manifestAndroidName(attrs[1]); name != "" {
			id := compID("feature", name)
			elements = append(elements, store.Element{
				QualifiedName: id,
				ElementType:   "android_feature",
				Name:          name,
				FilePath:      filePath,
				Language:      "android",
			})
			relationships = append(relationships, store.Relationship{
				Source: filePath, Target: id,
				RelType: "declares_feature", Confidence: 1.0,
				Metadata: map[string]any{"feature_name": name},
			})
		}
	}

	if m := manifestAppClassRe.FindStringSubmatch(content); m != nil {
		relationships = append(relationships, store.Relationship{
			Source: filePath, Target: m[1],
			RelType: "has_application_class", Confidence: 1.0,
			Metadata: map[string]any{"application_class": m[1]},
		})
	}

	for i, f := range manifestIntentFilter.FindAllStringSubmatch(content, -1) {
		fc := f[1]
		actions := collectCaptures(manifestActionRe, fc)
		categories := collectCaptures(manifestCategoryRe, fc)
		if len(actions) == 0 && len(categories) == 0 {
			continue
		}
		filterID := fmt.Sprintf("%s::intent_filter:%d", filePath, i)
		elements = append(elements, store.Element{
			QualifiedName: filterID,
			ElementType:   "android_intent_filter",
			Name:          fmt.Sprintf("intent_filter_%d", i),
			FilePath:      filePath,
			Language:      "android",
			Metadata:      map[string]any{"actions": actions, "categories": categories},
		})
		relationships = append(relationships, store.Relationship{
			Source: filePath, Target: filterID,
			RelType: "declares_intent_filter", Confidence: 1.0,
		})
	}

	for i, m := range manifestMetadataRe.FindAllStringSubmatch(content, -1) {
		name := m[1]
		metaID := fmt.Sprintf("%s::metadata:%d", filePath, i)
		elements = append(elements, store.Element{
			QualifiedName: metaID,
			ElementType:   "android_metadata",
			Name:          name,
			FilePath:      filePath,
			Language:      "android",
			Metadata: map[string]any{
				"value":    optionalCapture(m, 2),
				"resource": optionalCapture(m, 3),
			},
		})
		relationships = append(relationships, store.Relationship{
			Source: filePath, Target: metaID,
			RelType: "has_metadata", Confidence: 1.0,
			Metadata: map[string]any{"metadata_name": name},
		})
	}

	return elements, relationships
}

// manifestAndroidName pulls android:name="..." out of a tag's attribute text.
func manifestAndroidName(tagContent string) string {
	m := manifestNameRe.FindStringSubmatch(tagContent)
	if m == nil {
		return ""
	}
	return m[1]
}

func collectCaptures(re *regexp.Regexp, s string) []string {
	var out []string
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// optionalCapture returns capture group i as any (nil when absent), mirroring
// the Rust Option<String> JSON serialization.
func optionalCapture(m []string, i int) any {
	if m[i] == "" {
		return nil
	}
	return m[i]
}

// baseOf returns the final path element.
func baseOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}
