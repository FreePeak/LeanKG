package index

import (
	"regexp"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Ports of src/indexer/android_resource_refs.rs and
// src/indexer/android_resource_linker.rs (Rust reference f7624143^).
// Both are relationship-only Kotlin extractors.

var (
	refsRRefRe   = regexp.MustCompile(`R\.(\w+)\.(\w+)`)
	refsMethodRe = regexp.MustCompile(`(?:resources\.)?(?:getString|getText)\s*\(\s*R\.(\w+)\.(\w+)\s*\)`)
)

// refsResourceDir maps a resource type to its directory (parity with the
// Rust resource_dir).
func refsResourceDir(resType string) string {
	switch resType {
	case "string":
		return "values/strings.xml"
	case "drawable":
		return "drawable"
	case "layout":
		return "layout"
	case "id":
		return "values/ids.xml"
	case "color":
		return "values/colors.xml"
	case "style":
		return "values/styles.xml"
	case "dimen":
		return "values/dimens.xml"
	case "raw":
		return "raw"
	case "anim":
		return "anim"
	case "menu":
		return "menu"
	case "mipmap":
		return "mipmap"
	default:
		return "values"
	}
}

func refsRelType(resType string) string {
	switch resType {
	case "string":
		return "uses_string_resource"
	case "drawable":
		return "uses_drawable_resource"
	case "layout":
		return "uses_layout_resource"
	case "id":
		return "references_view_by_id"
	case "color":
		return "uses_color_resource"
	case "style":
		return "uses_style_resource"
	case "dimen":
		return "uses_dimen_resource"
	case "raw":
		return "uses_raw_resource"
	case "anim":
		return "uses_anim_resource"
	case "menu":
		return "uses_menu_resource"
	case "mipmap":
		return "uses_mipmap_resource"
	default:
		return "uses_resource"
	}
}

// ExtractResourceRefs extracts R.<type>.<name> and getString/getText resource
// references from Kotlin source. Results are deduped on
// (rel_type, target_qualified) like the Rust extractor.
func ExtractResourceRefs(filePath string, src []byte) []store.Relationship {
	content := string(src)
	var relationships []store.Relationship

	add := func(resType, resName, relType string, viaMethod bool) {
		meta := map[string]any{"resource_type": resType, "resource_name": resName}
		if viaMethod {
			meta["via_method"] = true
		}
		relationships = append(relationships, store.Relationship{
			Source:     filePath,
			Target:     "res/" + refsResourceDir(resType) + "/" + resName,
			RelType:    relType,
			Confidence: 1.0,
			Metadata:   meta,
		})
	}

	for _, m := range refsRRefRe.FindAllStringSubmatch(content, -1) {
		add(m[1], m[2], refsRelType(m[1]), false)
	}
	for _, m := range refsMethodRe.FindAllStringSubmatch(content, -1) {
		switch m[1] {
		case "string", "drawable", "color":
			add(m[1], m[2], "uses_"+m[1]+"_resource", true)
		}
	}

	// Dedup by (rel_type, target) — both extractors may match the same ref.
	seen := map[[2]string]bool{}
	out := relationships[:0]
	for _, r := range relationships {
		key := [2]string{r.RelType, r.Target}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	return out
}

var (
	linkSetContentRe = regexp.MustCompile(`setContentView\s*\(\s*R\.layout\.(\w+)\s*\)`)
	linkInflateRe    = regexp.MustCompile(`inflate\s*\(\s*R\.layout\.(\w+)`)
	linkBindingRe    = regexp.MustCompile(`(\w+Binding)\.(inflate|bind)\s*\(`)
	linkClickRe      = regexp.MustCompile(`(\w+)\.setOnClickListener\s*\{\s*([^}]+)\}`)
	linkFindClickRe  = regexp.MustCompile(`findViewById(?:<[^>]+>)?\s*\(\s*R\.id\.(\w+)\s*\)\.setOnClickListener`)
)

// ExtractResourceLinks links activities/fragments to layouts, view bindings
// and click handlers from Kotlin source.
func ExtractResourceLinks(filePath string, src []byte) []store.Relationship {
	content := string(src)
	var relationships []store.Relationship

	for _, m := range linkSetContentRe.FindAllStringSubmatch(content, -1) {
		relationships = append(relationships, store.Relationship{
			Source: filePath, Target: "res/layout/" + m[1] + ".xml",
			RelType: "inflates_layout", Confidence: 0.95,
			Metadata: map[string]any{"method": "setContentView", "layout_name": m[1]},
		})
	}

	for _, m := range linkInflateRe.FindAllStringSubmatch(content, -1) {
		relationships = append(relationships, store.Relationship{
			Source: filePath, Target: "res/layout/" + m[1] + ".xml",
			RelType: "inflates_layout", Confidence: 0.90,
			Metadata: map[string]any{"method": "inflate", "layout_name": m[1]},
		})
	}

	for _, m := range linkBindingRe.FindAllStringSubmatch(content, -1) {
		bindingName, method := m[1], m[2]
		relationships = append(relationships, store.Relationship{
			Source: filePath, Target: "generated/" + bindingName + ".java",
			RelType: "uses_viewbinding", Confidence: 0.95,
			Metadata: map[string]any{"binding_class": bindingName, "method": method},
		})
		// Infer the layout name from the binding class.
		layoutName := bindingToLayout(bindingName)
		relationships = append(relationships, store.Relationship{
			Source: filePath, Target: "res/layout/" + layoutName + ".xml",
			RelType: "inflates_layout", Confidence: 0.85,
			Metadata: map[string]any{"inferred_from_binding": bindingName, "layout_name": layoutName},
		})
	}

	for _, m := range linkClickRe.FindAllStringSubmatch(content, -1) {
		viewName := m[1]
		handlerBody := m[2]
		if rb := []rune(handlerBody); len(rb) > 50 {
			handlerBody = string(rb[:50])
		}
		relationships = append(relationships, store.Relationship{
			Source: filePath, Target: "__view__/" + viewName,
			RelType: "on_click_handler", Confidence: 0.80,
			Metadata: map[string]any{
				"view_id":              viewName,
				"handler_type":         "lambda",
				"handler_body_snippet": handlerBody,
			},
		})
	}

	for _, m := range linkFindClickRe.FindAllStringSubmatch(content, -1) {
		viewID := m[1]
		relationships = append(relationships, store.Relationship{
			Source: filePath, Target: "res/layout/__unknown__/@+id/" + viewID,
			RelType: "on_click_handler", Confidence: 0.85,
			Metadata: map[string]any{
				"view_id":      viewID,
				"method":       "findViewById",
				"handler_type": "lambda",
			},
		})
	}

	return relationships
}

// bindingToLayout converts a ViewBinding class name to its layout name:
// ActivityMainBinding -> activity_main.
func bindingToLayout(bindingName string) string {
	var b strings.Builder
	prevLower := false
	for i, c := range bindingName {
		// Stop at the "Binding" suffix.
		if strings.EqualFold(bindingName[i:], "binding") {
			break
		}
		if isUpperRune(c) && i > 0 && prevLower {
			b.WriteByte('_')
		}
		b.WriteRune(toLowerRune(c))
		prevLower = isLowerRune(c)
	}
	return strings.TrimSuffix(b.String(), "_binding")
}

func isUpperRune(r rune) bool { return r >= 'A' && r <= 'Z' }
func isLowerRune(r rune) bool { return r >= 'a' && r <= 'z' }

func toLowerRune(r rune) rune {
	if isUpperRune(r) {
		return r + ('a' - 'A')
	}
	return r
}
