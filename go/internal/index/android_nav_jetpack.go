package index

import (
	"regexp"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Port of src/indexer/android_nav_jetpack.rs (Rust reference f7624143^).
//
// Two entry points, like the Rust extractor:
//   - ExtractJetpackNavXML: res/navigation/*.xml navigation graphs, parsed
//     with encoding/xml (the Go replacement for roxmltree).
//   - ExtractJetpackNavKotlinDSL: Compose Navigation DSL (composable(route=...),
//     navigation(route=..., startDestination=...), navigate("...")) in Kotlin.
//
// Parity note: the Rust element ranges are roxmltree BYTE offsets stored in
// line_start/line_end; the XML path stores the same offsets, while the DSL
// path stores 1-based line numbers (as the Rust DSL path did).

const (
	nsAndroid = "http://schemas.android.com/apk/res/android"
	nsApp     = "http://schemas.android.com/apk/res-auto"
)

// ExtractJetpackNavXML extracts a Jetpack navigation graph from
// res/navigation/*.xml.
func ExtractJetpackNavXML(filePath string, src []byte) ([]store.Element, []store.Relationship) {
	var elements []store.Element
	var relationships []store.Relationship

	doc, err := decodeNavXML(src)
	if err != nil {
		return nil, nil
	}
	root := doc.Root
	if root == nil || root.Name.Local != "navigation" {
		return nil, nil
	}

	graphID := stripIDPrefix(androidXMLAttr(root, nsAndroid, "id"))
	if graphID == "" {
		graphID = "unknown"
	}
	graphQn := filePath + "::nav_graph::" + graphID

	var startDest any
	if raw := androidXMLAttr(root, nsApp, "startDestination"); raw != "" {
		startDest = stripIDPrefix(raw)
	}

	elements = append(elements, store.Element{
		QualifiedName: graphQn,
		ElementType:   "nav_graph",
		Name:          graphID,
		FilePath:      filePath,
		LineStart:     int(root.Start),
		LineEnd:       int(root.End),
		Language:      "xml",
		Metadata: map[string]any{
			"graph_id":          graphID,
			"start_destination": startDest,
		},
	})

	destTags := map[string]bool{"fragment": true, "activity": true, "dialog": true}

	for _, child := range root.Elements() {
		if !destTags[child.Name.Local] {
			continue
		}
		destID := stripIDPrefix(androidXMLAttr(child, nsAndroid, "id"))
		if destID == "" {
			continue
		}
		destQn := graphQn + "::" + destID
		isStart := startDest != nil && startDest == destID

		elements = append(elements, store.Element{
			QualifiedName:   destQn,
			ElementType:     "nav_destination",
			Name:            destID,
			FilePath:        filePath,
			LineStart:       int(child.Start),
			LineEnd:         int(child.End),
			Language:        "xml",
			ParentQualified: graphQn,
			Metadata: map[string]any{
				"destination_id":    destID,
				"dest_type":         child.Name.Local,
				"class_name":        optionalXMLAttr(child, nsAndroid, "name"),
				"start_destination": isStart,
			},
		})

		for _, sub := range child.Elements() {
			switch sub.Name.Local {
			case "action":
				target := stripIDPrefix(androidXMLAttr(sub, nsApp, "destination"))
				if target == "" {
					continue
				}
				relationships = append(relationships, store.Relationship{
					Source: destQn, Target: graphQn + "::" + target,
					RelType: "nav_action", Confidence: 1.0,
					Metadata: map[string]any{
						"action_id": optionalStrippedID(sub, nsAndroid, "id"),
						"pop_up_to": optionalXMLAttr(sub, nsApp, "popUpTo"),
					},
				})
			case "argument":
				argName := androidXMLAttr(sub, nsAndroid, "name")
				if argName == "" {
					continue
				}
				argQn := destQn + "::arg::" + argName
				argType := androidXMLAttr(sub, nsApp, "argType")
				if argType == "" {
					argType = "string"
				}
				nullable := androidXMLAttr(sub, nsApp, "nullable") == "true"

				elements = append(elements, store.Element{
					QualifiedName:   argQn,
					ElementType:     "nav_argument",
					Name:            argName,
					FilePath:        filePath,
					LineStart:       int(sub.Start),
					LineEnd:         int(sub.End),
					Language:        "xml",
					ParentQualified: destQn,
					Metadata:        map[string]any{"arg_type": argType, "nullable": nullable},
				})
				relationships = append(relationships, store.Relationship{
					Source: destQn, Target: argQn,
					RelType: "requires_arg", Confidence: 1.0,
					Metadata: map[string]any{"arg_name": argName},
				})
			case "deepLink":
				uri := androidXMLAttr(sub, nsApp, "uri")
				if uri == "" {
					continue
				}
				dlQn := destQn + "::deeplink::" + uri
				elements = append(elements, store.Element{
					QualifiedName:   dlQn,
					ElementType:     "nav_deep_link",
					Name:            uri,
					FilePath:        filePath,
					LineStart:       int(sub.Start),
					LineEnd:         int(sub.End),
					Language:        "xml",
					ParentQualified: destQn,
					Metadata:        map[string]any{"uri": uri},
				})
				relationships = append(relationships, store.Relationship{
					Source: dlQn, Target: destQn,
					RelType: "deep_link", Confidence: 1.0,
				})
			}
		}
	}

	return elements, relationships
}

// decodeNavXML decodes src as XML, injecting the android namespace declaration
// when the content uses android: attributes without declaring the namespace
// (roxmltree would reject undeclared prefixes; real nav graphs always declare
// it, small fixtures often do not).
func decodeNavXML(src []byte) (*xmlDoc, error) {
	content := string(src)
	if strings.Contains(content, "android:") && !strings.Contains(content, "xmlns:android") {
		content = strings.Replace(content, "<navigation",
			`<navigation xmlns:android="`+nsAndroid+`"`, 1)
	}
	return parseXML([]byte(content))
}

var (
	jetpackComposableRe = regexp.MustCompile(`composable\s*\(\s*route\s*=\s*"([^"]+)"`)
	jetpackNavBlockRe   = regexp.MustCompile(`navigation\s*\(\s*route\s*=\s*"([^"]+)"\s*,\s*startDestination\s*=\s*"([^"]+)"`)
	jetpackArgRe        = regexp.MustCompile(`argument\s*\(\s*name\s*=\s*"([^"]+)"`)
	jetpackNavigateRe   = regexp.MustCompile(`navigate\s*\(\s*"([^"]+)"`)
)

// navBlock is one composable/navigation destination block with its line range.
type navBlock struct {
	destQn    string
	startLine int
	endLine   int
}

// ExtractJetpackNavKotlinDSL parses Compose Navigation DSL from Kotlin source.
func ExtractJetpackNavKotlinDSL(filePath string, src []byte) ([]store.Element, []store.Relationship) {
	content := string(src)
	var elements []store.Element
	var relationships []store.Relationship

	graphID := "compose_nav"
	graphQn := filePath + "::nav_graph::" + graphID

	lastLine := 1
	if lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n"); len(lines) > 0 {
		lastLine = len(lines[len(lines)-1]) + 1
	}
	elements = append(elements, store.Element{
		QualifiedName: graphQn,
		ElementType:   "nav_graph",
		Name:          graphID,
		FilePath:      filePath,
		LineStart:     1,
		LineEnd:       lastLine,
		Language:      "kotlin",
		Metadata:      map[string]any{"graph_id": graphID, "dsl_type": "compose"},
	})

	var blocks []navBlock

	addBlock := func(route, destType string, startDest any, m []int) {
		if len(m) < 4 {
			return
		}
		destQn := graphQn + "::" + route
		startLine, endLine := navBlockRange(content, m[0])
		blocks = append(blocks, navBlock{destQn: destQn, startLine: startLine, endLine: endLine})
		meta := map[string]any{
			"destination_id": route,
			"dest_type":      destType,
			"route":          route,
		}
		if startDest != nil {
			meta["start_destination"] = startDest
		}
		elements = append(elements, store.Element{
			QualifiedName:   destQn,
			ElementType:     "nav_destination",
			Name:            route,
			FilePath:        filePath,
			LineStart:       startLine,
			LineEnd:         endLine,
			Language:        "kotlin",
			ParentQualified: graphQn,
			Metadata:        meta,
		})
	}

	for _, m := range jetpackComposableRe.FindAllStringSubmatchIndex(content, -1) {
		addBlock(content[m[2]:m[3]], "composable", nil, m)
	}
	for _, m := range jetpackNavBlockRe.FindAllStringSubmatchIndex(content, -1) {
		addBlock(content[m[2]:m[3]], "navigation", content[m[4]:m[5]], m)
	}

	// Global argument pass: each argument attaches to the last block that ends
	// after the argument's line.
	for _, m := range jetpackArgRe.FindAllStringSubmatchIndex(content, -1) {
		argName := content[m[2]:m[3]]
		argLine := rustLineCount(content[:m[0]])
		var block *navBlock
		for i := len(blocks) - 1; i >= 0; i-- {
			if blocks[i].endLine > argLine {
				block = &blocks[i]
				break
			}
		}
		if block == nil {
			continue
		}
		argQn := block.destQn + "::arg::" + argName
		elements = append(elements, store.Element{
			QualifiedName:   argQn,
			ElementType:     "nav_argument",
			Name:            argName,
			FilePath:        filePath,
			LineStart:       argLine,
			LineEnd:         argLine,
			Language:        "kotlin",
			ParentQualified: block.destQn,
			Metadata:        map[string]any{"arg_type": "string", "nullable": false},
		})
		relationships = append(relationships, store.Relationship{
			Source: block.destQn, Target: argQn,
			RelType: "requires_arg", Confidence: 0.85,
			Metadata: map[string]any{"arg_name": argName},
		})
	}

	// navigate("route") calls link from the nearest preceding block.
	seen := map[string]bool{}
	for _, m := range jetpackNavigateRe.FindAllStringSubmatchIndex(content, -1) {
		targetRoute := content[m[2]:m[3]]
		if seen[targetRoute] || len(blocks) == 0 {
			continue
		}
		seen[targetRoute] = true
		navLine := rustLineCount(content[:m[0]])
		var source *navBlock
		for i := len(blocks) - 1; i >= 0; i-- {
			if blocks[i].endLine < navLine {
				source = &blocks[i]
				break
			}
		}
		if source != nil {
			relationships = append(relationships, store.Relationship{
				Source: source.destQn, Target: graphQn + "::" + targetRoute,
				RelType: "nav_action", Confidence: 0.75,
				Metadata: map[string]any{
					"action_id":    nil,
					"source_line":  source.startLine,
					"target_route": targetRoute,
				},
			})
		} else if len(elements) > 0 {
			// Fallback: the first element (the graph) is the source.
			relationships = append(relationships, store.Relationship{
				Source: elements[0].QualifiedName, Target: graphQn + "::" + targetRoute,
				RelType: "nav_action", Confidence: 0.75,
				Metadata: map[string]any{"action_id": nil, "target_route": targetRoute},
			})
		}
	}

	return elements, relationships
}

// navBlockRange returns the (1-based) start line of the block header at byte
// offset start and the line of its matching closing brace. Parity note: the
// depth counter starts at 1 (as in the Rust reference), so when the header is
// nested the end is the enclosing scope's closing brace.
func navBlockRange(content string, start int) (int, int) {
	startLine := rustLineCount(content[:start])
	after := content[start:]
	depth := 1
	for i := 0; i < len(after); i++ {
		switch after[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return startLine, rustLineCount(content[:start+i+1])
			}
		}
	}
	return startLine, rustLineCount(content)
}

// rustLineCount mirrors Rust's str::lines().count() for a prefix: a trailing
// newline does not open a new line.
func rustLineCount(prefix string) int {
	n := strings.Count(prefix, "\n")
	if prefix != "" && !strings.HasSuffix(prefix, "\n") {
		n++
	}
	return n
}
