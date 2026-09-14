package index

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Port of src/indexer/android_hilt.rs (Rust reference f7624143^): Hilt
// dependency-injection patterns — @Module classes, @Provides functions,
// @Inject constructor and field injection.

var (
	hiltModuleRe   = regexp.MustCompile(`(?s)@Module\s*\n?\s*(?:@InstallIn\(.*?\)\s*\n?\s*)?(?:abstract\s+)?(?:class|object)\s+(\w+)`)
	hiltProvidesRe = regexp.MustCompile(`@Provides\s*\n?(?:@Singleton\s*\n?)?\s*fun\s+(\w+)\s*\([^)]*\)\s*:\s*([^={\n]+)`)
	hiltInjectRe   = regexp.MustCompile(`class\s+(\w+).*?@Inject\s*\n?\s*constructor\s*\(([^)]+)\)`)
	hiltParamRe    = regexp.MustCompile(`(\w+)\s*:\s*(\w+)`)
	hiltFieldRe    = regexp.MustCompile(`@Inject\s*\n?\s*(?:lateinit\s+)?var\s+(\w+)\s*:\s*(\w+)`)
)

// ExtractHilt extracts Hilt modules, providers and injection relationships
// from Kotlin source.
func ExtractHilt(filePath string, src []byte) ([]store.Element, []store.Relationship) {
	content := string(src)
	var elements []store.Element
	var relationships []store.Relationship

	// Modules.
	var modules []store.Element
	for _, m := range hiltModuleRe.FindAllStringSubmatch(content, -1) {
		moduleName := m[1]
		modules = append(modules, store.Element{
			QualifiedName: filePath + "::HiltModule:" + moduleName,
			ElementType:   "hilt_module",
			Name:          moduleName,
			FilePath:      filePath,
			Language:      "kotlin",
			Metadata:      map[string]any{"class_name": moduleName},
		})
	}
	elements = append(elements, modules...)

	// Module body spans (for provider containment).
	type moduleSpan struct {
		elem       store.Element
		start, end int
	}
	var spans []moduleSpan
	for _, mod := range modules {
		re, err := regexp.Compile(`(?s)@Module[^{]*?(?:class|object)\s+` + regexp.QuoteMeta(mod.Name) + `\b`)
		if err != nil {
			continue
		}
		if loc := re.FindStringIndex(content); loc != nil {
			spans = append(spans, moduleSpan{elem: mod, start: loc[0], end: findClassBodyEnd(content, loc[0])})
		}
	}

	// Providers.
	for _, m := range hiltProvidesRe.FindAllStringSubmatchIndex(content, -1) {
		if len(m) < 6 {
			continue
		}
		providerName, returnType := content[m[2]:m[3]], content[m[4]:m[5]]
		qualifiedName := filePath + "::HiltProvider:" + providerName
		providerPos := m[0]

		cleanType := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(
			strings.Split(returnType, "=")[0]), "{"))
		cleanType = strings.TrimSpace(cleanType)

		elements = append(elements, store.Element{
			QualifiedName: qualifiedName,
			ElementType:   "hilt_provider",
			Name:          providerName,
			FilePath:      filePath,
			Language:      "kotlin",
			Metadata: map[string]any{
				"method_name":   providerName,
				"provides_type": cleanType,
			},
		})

		for _, sp := range spans {
			if providerPos >= sp.start && providerPos < sp.end {
				relationships = append(relationships, store.Relationship{
					Source: sp.elem.QualifiedName, Target: qualifiedName,
					RelType: "hilt_module_provides", Confidence: 0.9,
				})
				break
			}
		}

		relationships = append(relationships, store.Relationship{
			Source: qualifiedName, Target: "__type__" + returnType,
			RelType: "hilt_provides", Confidence: 0.9,
			Metadata: map[string]any{"provided_type": returnType},
		})
	}

	// Constructor injection.
	for _, m := range hiltInjectRe.FindAllStringSubmatch(content, -1) {
		className, params := m[1], m[2]
		for _, pm := range hiltParamRe.FindAllStringSubmatch(params, -1) {
			typeName := pm[2]
			relationships = append(relationships, store.Relationship{
				Source:  fmt.Sprintf("%s::__class__%s", filePath, className),
				Target:  "__type__" + typeName,
				RelType: "hilt_injected", Confidence: 0.8,
				Metadata: map[string]any{"injected_type": typeName},
			})
		}
	}

	// Field injection.
	for _, m := range hiltFieldRe.FindAllStringSubmatch(content, -1) {
		fieldName, fieldType := m[1], m[2]
		relationships = append(relationships, store.Relationship{
			Source:  filePath,
			Target:  "__type__" + fieldType,
			RelType: "hilt_field_injected", Confidence: 0.8,
			Metadata: map[string]any{"field_name": fieldName, "field_type": fieldType},
		})
	}

	return elements, relationships
}
