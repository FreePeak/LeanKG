package index

import (
	"regexp"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Port of src/indexer/android_nav_fragments.rs and android_nav_leanback.rs
// (Rust reference f7624143^). Both are relationship-only Kotlin extractors,
// except the leanback one also emits nav_destination elements.

var (
	fragNavReplaceRe = regexp.MustCompile(`\.(?:replace|add)\s*\(\s*[^,]+,\s*(\w+Fragment)\s*\(`)
	fragNavStackRe   = regexp.MustCompile(`\.addToBackStack\s*\(\s*"([^"]+)"\s*\)`)
	fragNavStartRe   = regexp.MustCompile(`startActivity\s*\(\s*Intent\s*\([^,]+,\s*(\w+Activity)::class\.java\s*\)`)
	fragNavCtrlRe    = regexp.MustCompile(`(?:findNavController|navController)\s*\(\s*\)\s*\.navigate\s*\(\s*(?:R\.id\.([\w_]+)|"([^"]+)")`)
)

// ExtractFragmentNav extracts fragment/activity navigation relationships from
// Kotlin source (FragmentManager transactions, startActivity, NavController).
func ExtractFragmentNav(filePath string, src []byte) []store.Relationship {
	content := string(src)
	var relationships []store.Relationship

	for _, m := range fragNavReplaceRe.FindAllStringSubmatch(content, -1) {
		fragmentName := m[1]
		targetQn := "class:" + fragmentName

		// Backstack tag: first addToBackStack within 300 bytes of the match.
		pos := strings.Index(content, m[0])
		var backstackTag any
		if pos >= 0 {
			end := pos + 300
			if end > len(content) {
				end = len(content)
			}
			if bm := fragNavStackRe.FindStringSubmatch(content[pos:end]); bm != nil {
				backstackTag = bm[1]
			}
		}

		relationships = append(relationships, store.Relationship{
			Source: filePath, Target: targetQn,
			RelType: "navigates_to", Confidence: 0.85,
			Metadata: map[string]any{
				"nav_type":      "fragment_manager",
				"fragment_name": fragmentName,
				"backstack_tag": backstackTag,
			},
		})
	}

	for _, m := range fragNavStartRe.FindAllStringSubmatch(content, -1) {
		activityName := m[1]
		relationships = append(relationships, store.Relationship{
			Source: filePath, Target: "class:" + activityName,
			RelType: "navigates_to", Confidence: 0.90,
			Metadata: map[string]any{
				"nav_type":      "start_activity",
				"activity_name": activityName,
			},
		})
	}

	for _, m := range fragNavCtrlRe.FindAllStringSubmatch(content, -1) {
		actionOrRoute := m[1]
		if actionOrRoute == "" {
			actionOrRoute = m[2]
		}
		if actionOrRoute == "" {
			continue
		}
		relationships = append(relationships, store.Relationship{
			Source: filePath, Target: "nav_action:" + actionOrRoute,
			RelType: "navigates_to", Confidence: 0.85,
			Metadata: map[string]any{
				"nav_type":        "nav_controller",
				"action_or_route": actionOrRoute,
			},
		})
	}

	return relationships
}

var (
	leanbackBrowseRe = regexp.MustCompile(`class\s+(\w+)\s*:\s*(?:BrowseSupportFragment|BrowseFragment|VerticalGridSupportFragment)\s*\(`)
	leanbackStartRe  = regexp.MustCompile(`startActivity\s*\(\s*Intent\s*\([^,]+,\s*(\w+Activity)::class\.java\s*\)`)
	leanbackClickRe  = regexp.MustCompile(`setOnItemViewClickedListener\b`)
	leanbackFragRe   = regexp.MustCompile(`(\w+Fragment)\s*\(`)
)

// ExtractLeanbackNav extracts Leanback TV navigation: browse fragments as
// nav_destination elements plus presents relationships to details activities
// and click-target fragments.
func ExtractLeanbackNav(filePath string, src []byte) ([]store.Element, []store.Relationship) {
	content := string(src)
	var elements []store.Element
	var relationships []store.Relationship

	for _, m := range leanbackBrowseRe.FindAllStringSubmatch(content, -1) {
		className := m[1]
		elemQn := filePath + "::" + className

		elements = append(elements, store.Element{
			QualifiedName: elemQn,
			ElementType:   "nav_destination",
			Name:          className,
			FilePath:      filePath,
			Language:      "kotlin",
			Metadata: map[string]any{
				"dest_type":  "leanback_browse",
				"class_name": className,
			},
		})

		for _, dm := range leanbackStartRe.FindAllStringSubmatch(content, -1) {
			activityName := dm[1]
			relationships = append(relationships, store.Relationship{
				Source: elemQn, Target: "class:" + activityName,
				RelType: "presents", Confidence: 0.80,
				Metadata: map[string]any{
					"nav_type":      "leanback_browse_to_details",
					"activity_name": activityName,
				},
			})
		}

		if loc := leanbackClickRe.FindStringIndex(content); loc != nil {
			end := loc[0] + 500
			if end > len(content) {
				end = len(content)
			}
			for _, fm := range leanbackFragRe.FindAllStringSubmatch(content[loc[0]:end], -1) {
				fragName := fm[1]
				if fragName == className {
					continue
				}
				relationships = append(relationships, store.Relationship{
					Source: elemQn, Target: "class:" + fragName,
					RelType: "presents", Confidence: 0.75,
					Metadata: map[string]any{
						"nav_type":      "leanback_item_click",
						"fragment_name": fragName,
					},
				})
			}
		}
	}

	return elements, relationships
}
