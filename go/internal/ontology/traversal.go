package ontology

import (
	"sort"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Ontology-guided top-down graph traversal for provenance (port of
// retrieval/ontology_traversal.rs, non-embedding parts).
//
// Given upper nodes (class / file / workflow / domain concept / ...) that
// matched an intent, walk DOWN the graph from each one and discover the
// function nodes that implement it. Each discovered function carries its
// provenance: which upper node it came from, over which edge, at which
// hop. Traversal stops at function targets — we never expand through a
// function to its callees.

// FunctionTargetTypes are the element types treated as the deliverable of
// a downward walk.
var FunctionTargetTypes = map[string]bool{
	"function": true, "method": true, "constructor": true,
}

// UpperTypes are the element types that can plausibly own / document /
// implement a function.
var UpperTypes = map[string]bool{
	"class": true, "struct": true, "interface": true, "trait": true,
	"module": true, "file": true, "document": true, "doc_section": true,
	TypeWorkflow: true, TypeWorkflowStep: true, TypeDecisionPoint: true,
	TypeFailureMode:  true,
	TypeDomainEntity: true, TypeService: true, TypeAPIEndpoint: true,
	TypeDataStore: true, TypeKnownIssue: true, TypePlaybook: true,
	TypePlaybookStep: true, TypeTeamKnowledge: true,
}

// IsFunctionTarget reports whether element_type is a downward-walk target.
func IsFunctionTarget(elementType string) bool { return FunctionTargetTypes[elementType] }

// IsUpperType reports whether element_type is a valid upper seed.
func IsUpperType(elementType string) bool { return UpperTypes[elementType] }

// indexerNoiseTypes are element types that never appear as traversal
// neighbors (parity with INDEXER_NOISE_TYPES).
var indexerNoiseTypes = map[string]bool{"unknown": true, TypeEnvironment: true}

// IsIndexerNoise reports whether an element type is traversal noise.
func IsIndexerNoise(elementType string) bool { return indexerNoiseTypes[elementType] }

// DownwardRule is the per-type downward traversal policy: hop budget,
// allowed edge types and fanout cap (parity with DownwardRule).
type DownwardRule struct {
	Hops      int
	EdgeTypes map[string]bool
	FanoutCap int
}

func edges(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// DownwardRuleFor returns the traversal rule for an upper-seed element
// type (parity with downward_rule_for; tuning rationale in the Rust
// reference).
func DownwardRuleFor(elementType string) DownwardRule {
	switch elementType {
	case "class", "struct", "interface", "trait":
		return DownwardRule{Hops: 1, EdgeTypes: edges("contains", "defines", "has_method", "has_property"), FanoutCap: 12}
	case "module":
		// module → file_summary via contains, then file → function: 2 hops.
		return DownwardRule{Hops: 2, EdgeTypes: edges("contains", "defines", "has_method", "has_property"), FanoutCap: 16}
	case "file":
		return DownwardRule{Hops: 1, EdgeTypes: edges("contains", "defines", "imports", "references", "tested_by", "documented_by"), FanoutCap: 12}
	case "document":
		return DownwardRule{Hops: 1, EdgeTypes: edges("references", "documented_by"), FanoutCap: 10}
	case "doc_section":
		// Sections carry no direct references: contains to the document,
		// then the document's references/documented_by down to code.
		return DownwardRule{Hops: 2, EdgeTypes: edges("contains", "references", "documented_by"), FanoutCap: 10}
	case TypeWorkflow:
		return DownwardRule{Hops: 2, EdgeTypes: edges(
			RelHasStep, RelNextStep, RelBranchesTo, RelImplementedBy,
			"entry_point_of", "step_in_process", RelHasFailureMode), FanoutCap: 15}
	case TypeWorkflowStep, TypeDecisionPoint, TypeFailureMode:
		return DownwardRule{Hops: 1, EdgeTypes: edges(
			RelNextStep, RelBranchesTo, RelImplementedBy,
			RelHandledByPlaybook, RelHasFailureMode, RelResolvedByPlaybook), FanoutCap: 12}
	case TypeDomainEntity, TypeService, TypeAPIEndpoint, TypeDataStore:
		return DownwardRule{Hops: 2, EdgeTypes: edges(
			RelOwnsConcept, RelImplementsConcept, RelExposesEndpoint,
			RelReadsFrom, RelWritesTo, RelDocumentsConcept, RelHasKnownIssue), FanoutCap: 12}
	case TypeKnownIssue, TypePlaybook, TypePlaybookStep, TypeTeamKnowledge:
		return DownwardRule{Hops: 1, EdgeTypes: edges(
			RelHasKnownIssue, RelResolvedByPlaybook, RelDocumentsConcept), FanoutCap: 8}
	default:
		return DownwardRule{Hops: 1, EdgeTypes: edges("documented_by", RelDocumentsConcept), FanoutCap: 5}
	}
}

// GlobalFunctionCap caps total discovered functions across all upper
// seeds (parity with GLOBAL_FUNCTION_CAP).
const GlobalFunctionCap = 80

// UpperSeed is an upper node to traverse down from.
type UpperSeed struct {
	QualifiedName string
	ElementType   string
	Name          string
}

// NewUpperSeed builds a seed, deriving a display name from the QN when
// none is supplied (parity with UpperSeed::new / derive_display_name).
func NewUpperSeed(qualifiedName, elementType string) UpperSeed {
	return UpperSeed{
		QualifiedName: qualifiedName,
		ElementType:   elementType,
		Name:          DeriveDisplayName(qualifiedName),
	}
}

// DeriveDisplayName derives a human-readable name from a qualified name:
// ontology GID → the id part; path::Symbol → Symbol; else the trailing
// / or : segment (parity with derive_display_name).
func DeriveDisplayName(qualifiedName string) string {
	if rest, ok := strings.CutPrefix(qualifiedName, elementFilePrefix); ok {
		if g, ok := ParseGid(rest); ok && g.ID != "" {
			return g.ID
		}
	}
	if idx := strings.LastIndexAny(qualifiedName, "/:"); idx >= 0 && idx+1 < len(qualifiedName) {
		return qualifiedName[idx+1:]
	}
	// No separator, or a trailing separator yielding an empty segment: the
	// whole string is the name (parity with Rust rsplit().next() +
	// filter(non-empty) + unwrap_or(whole)).
	return qualifiedName
}

// DiscoveredFunction is a function found by downward traversal, with the
// provenance of how it was reached (parity with DiscoveredFunction).
type DiscoveredFunction struct {
	QualifiedName string `json:"qualified_name"`
	ElementType   string `json:"element_type"`
	FilePath      string `json:"file_path"`
	ViaUpper      string `json:"via_upper"`
	ViaUpperType  string `json:"via_upper_type"`
	ViaUpperName  string `json:"via_upper_name"`
	ViaEdge       string `json:"via_edge"`
	Hop           int    `json:"hop"`
}

// TraverseToFunctions walks down from each upper seed, collecting function
// nodes via the per-type rule. Branches terminate at function targets.
// Deduplicates by function QN keeping the shortest-hop provenance; honors
// GlobalFunctionCap. When BFS yields nothing for a seed whose code links
// live in metadata.code_refs, falls back to keyed path-prefix resolution
// (parity with traverse_to_functions).
func TraverseToFunctions(st store.Backend, seeds []UpperSeed, env string) ([]DiscoveredFunction, error) {
	var discovered []DiscoveredFunction
	seen := map[string]int{} // function QN -> index into discovered
	total := 0

	for _, upper := range seeds {
		if total >= GlobalFunctionCap {
			break
		}
		rule := DownwardRuleFor(upper.ElementType)
		seedCap := rule.FanoutCap
		if remaining := GlobalFunctionCap - total; seedCap > remaining {
			seedCap = remaining
		}

		foundForSeed := 0
		visited := map[string]bool{upper.QualifiedName: true}
		type frontierItem struct {
			qn  string
			hop int
		}
		frontier := []frontierItem{{upper.QualifiedName, 0}}

		for len(frontier) > 0 {
			if foundForSeed >= seedCap || total >= GlobalFunctionCap {
				break
			}
			item := frontier[0]
			frontier = frontier[1:]
			if item.hop >= rule.Hops {
				continue
			}

			outgoing, err := st.Outgoing(item.qn)
			if err != nil {
				return nil, err
			}
			incoming, err := st.Incoming(item.qn)
			if err != nil {
				return nil, err
			}
			rels := append(outgoing, incoming...)

			for _, rel := range rels {
				if foundForSeed >= seedCap || total >= GlobalFunctionCap {
					break
				}
				if !rule.EdgeTypes[rel.RelType] {
					continue
				}
				neighbor := rel.Target
				if neighbor == item.qn {
					neighbor = rel.Source
				}
				if neighbor == "" || visited[neighbor] {
					continue
				}
				visited[neighbor] = true

				el, ok, err := findElementByQN(st, neighbor)
				if err != nil {
					return nil, err
				}
				if !ok || IsIndexerNoise(el.ElementType) {
					continue
				}

				nextHop := item.hop + 1
				if IsFunctionTarget(el.ElementType) {
					recordFunction(&discovered, seen, &total, el,
						upper, rel.RelType, nextHop)
					foundForSeed++
					continue
				}
				if nextHop < rule.Hops {
					frontier = append(frontier, frontierItem{neighbor, nextHop})
				}
			}
		}

		if foundForSeed == 0 && needsCodeRefsFallback(upper.ElementType) {
			if err := resolveCodeRefsFallback(st, upper, env, seedCap,
				&discovered, seen, &total); err != nil {
				return nil, err
			}
		}
	}
	return discovered, nil
}

// needsCodeRefsFallback is true for upper-seed types whose code links
// typically live in metadata.code_refs rather than DB edges (parity with
// needs_code_refs_fallback).
func needsCodeRefsFallback(elementType string) bool {
	switch elementType {
	case TypeDomainEntity, TypeService, TypeAPIEndpoint, TypeDataStore,
		TypeWorkflow, TypeWorkflowStep, TypeKnownIssue, TypePlaybook,
		TypeTeamKnowledge:
		return true
	}
	return false
}

// resolveCodeRefsFallback resolves metadata.code_refs of an upper seed
// into concrete function nodes (parity with resolve_code_refs_fallback).
func resolveCodeRefsFallback(st store.Backend, upper UpperSeed, env string, fanoutCap int,
	discovered *[]DiscoveredFunction, seen map[string]int, total *int) error {

	el, ok, err := findElementByQN(st, upper.QualifiedName)
	if err != nil || !ok {
		return err
	}
	codeRefs := jsonStrArray(el.Metadata, "code_refs")
	if len(codeRefs) == 0 {
		return nil
	}

	matched := 0
	for _, rawRef := range codeRefs {
		if matched >= fanoutCap || *total >= GlobalFunctionCap {
			break
		}
		// Exact QN first, then file::symbol / path forms.
		resolved, err := ResolveCodeRefs(st, []string{rawRef}, fanoutCap-matched)
		if err != nil {
			return err
		}
		for _, cand := range resolved {
			if matched >= fanoutCap || *total >= GlobalFunctionCap {
				break
			}
			if !IsFunctionTarget(cand.ElementType) {
				continue
			}
			recordFunction(discovered, seen, total, cand, upper, "code_ref", 1)
			matched++
		}
	}
	return nil
}

// recordFunction inserts a discovered function, deduplicating by QN and
// keeping the shortest-hop provenance (parity with record_function).
func recordFunction(discovered *[]DiscoveredFunction, seen map[string]int, total *int,
	el store.Element, upper UpperSeed, viaEdge string, hop int) {

	if idx, ok := seen[el.QualifiedName]; ok {
		if hop < (*discovered)[idx].Hop {
			(*discovered)[idx] = DiscoveredFunction{
				QualifiedName: el.QualifiedName,
				ElementType:   el.ElementType,
				FilePath:      el.FilePath,
				ViaUpper:      upper.QualifiedName,
				ViaUpperType:  upper.ElementType,
				ViaUpperName:  upper.Name,
				ViaEdge:       viaEdge,
				Hop:           hop,
			}
		}
		return
	}
	*discovered = append(*discovered, DiscoveredFunction{
		QualifiedName: el.QualifiedName,
		ElementType:   el.ElementType,
		FilePath:      el.FilePath,
		ViaUpper:      upper.QualifiedName,
		ViaUpperType:  upper.ElementType,
		ViaUpperName:  upper.Name,
		ViaEdge:       viaEdge,
		Hop:           hop,
	})
	seen[el.QualifiedName] = len(*discovered) - 1
	*total++
}

// CompositeText is the text re-embedded to score a discovered function
// against the original intent (parity with composite_text).
func CompositeText(upperName, funcBlob string) string {
	if funcBlob == "" {
		return upperName
	}
	return upperName + "\n" + funcBlob
}

// SortDiscovered orders discovered functions by hop then qualified name —
// a deterministic presentation order for MCP output.
func SortDiscovered(fns []DiscoveredFunction) {
	sort.SliceStable(fns, func(i, j int) bool {
		if fns[i].Hop != fns[j].Hop {
			return fns[i].Hop < fns[j].Hop
		}
		return fns[i].QualifiedName < fns[j].QualifiedName
	})
}
