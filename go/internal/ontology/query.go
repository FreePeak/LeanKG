package ontology

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// NodeInfo is one ontology node hit with its match score and reason
// (parity with OntologyNodeInfo).
type NodeInfo struct {
	GID           string   `json:"gid"`
	Name          string   `json:"name"`
	ElementType   string   `json:"element_type"`
	Description   string   `json:"description"`
	Aliases       []string `json:"aliases"`
	OntologyLayer string   `json:"ontology_layer"`
	MatchScore    float64  `json:"match_score"`
	MatchReason   string   `json:"match_reason"`
}

// ContextResult is the full ontology context for a semantic query (parity
// with OntologyContextResult).
type ContextResult struct {
	MatchedNodes        []NodeInfo           `json:"matched_ontology_nodes"`
	ExpandedCodeContext []store.Element      `json:"expanded_code_context"`
	ExpandedRelations   []store.Relationship `json:"expanded_relationships"`
	Workflows           []WorkflowNode       `json:"workflows"`
	WorkflowSteps       []WorkflowStepNode   `json:"workflow_steps"`
	FailureModes        []FailureModeNode    `json:"failure_modes"`
	Confidence          float64              `json:"confidence"`
	MatchReasons        []string             `json:"match_reasons"`
}

// IsEmpty reports whether any ontology node matched.
func (r ContextResult) IsEmpty() bool { return len(r.MatchedNodes) == 0 }

// MatchedConcept is a concept matched by ConceptSearch with its declared
// code references attached (parity with MatchedConcept).
type MatchedConcept struct {
	GID         string   `json:"gid"`
	Name        string   `json:"name"`
	ElementType string   `json:"element_type"`
	Description string   `json:"description"`
	Aliases     []string `json:"aliases"`
	MatchScore  float64  `json:"match_score"`
	MatchReason string   `json:"match_reason"`
	CodeRefs    []string `json:"code_refs"`
	Docs        []string `json:"docs"`
	OwnedBy     []string `json:"owned_by"`
}

// ConceptSearchResult is the concept-gated search workflow result:
// extract keywords -> scan concept ontology -> load concept -> resolve
// code_refs against indexed elements (parity with ConceptSearchResult).
type ConceptSearchResult struct {
	Query             string           `json:"query"`
	ExtractedKeywords []string         `json:"extracted_keywords"`
	MatchedConcepts   []MatchedConcept `json:"matched_concepts"`
	LinkedCode        []store.Element  `json:"linked_code"`
	ConceptMatchCount int              `json:"concept_match_count"`
	CodeRefCount      int              `json:"code_ref_count"`
	LinkedCodeCount   int              `json:"linked_code_count"`
	FallbackUsed      bool             `json:"fallback_used"`
	FallbackResults   []store.Element  `json:"fallback_results"`
}

// Status is the ontology status payload (parity with OntologyStatus).
type Status struct {
	ConceptCounts                map[string]int `json:"concept_counts"`
	ProceduralCounts             map[string]int `json:"procedural_counts"`
	TotalAliases                 int            `json:"total_aliases"`
	NodesMissingAliases          int            `json:"nodes_missing_aliases"`
	WorkflowsWithoutFailureModes int            `json:"workflows_without_failure_modes"`
	DynamicConcepts              int            `json:"dynamic_concepts"`
	DynamicWorkflows             int            `json:"dynamic_workflows"`
}

// SearchOntologyNodes searches ontology elements by query string against
// name, aliases and description, returning scored hits sorted descending
// (parity with search_ontology_nodes). depth is accepted for API parity
// but the search itself is depth-independent.
func SearchOntologyNodes(st store.Backend, query string) ([]NodeInfo, error) {
	els, err := ontologyElements(st)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(query)
	matches := make([]NodeInfo, 0)
	for _, el := range els {
		aliases := jsonStrArray(el.Metadata, "aliases")
		description, _ := el.Metadata["description"].(string)
		layer, _ := el.Metadata["ontology_layer"].(string)
		if layer == "" {
			layer = "domain"
		}
		score, reason := MatchScore(q, el.Name, aliases, description)
		if score > 0 {
			matches = append(matches, NodeInfo{
				GID:           el.QualifiedName,
				Name:          el.Name,
				ElementType:   el.ElementType,
				Description:   description,
				Aliases:       aliases,
				OntologyLayer: layer,
				MatchScore:    score,
				MatchReason:   reason,
			})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].MatchScore > matches[j].MatchScore
	})
	return matches, nil
}

// ontologyElements returns every stored ontology element (file_path
// ontology://) ordered by qualified name.
func ontologyElements(st store.Backend) ([]store.Element, error) {
	els, err := st.Elements()
	if err != nil {
		return nil, err
	}
	out := make([]store.Element, 0, len(els)/8)
	for _, el := range els {
		if IsOntologyFilePath(el.FilePath) {
			out = append(out, el)
		}
	}
	return out, nil
}

// findElementByQN finds a single element by qualified name (any layer).
func findElementByQN(st store.Backend, qn string) (store.Element, bool, error) {
	// FindExact matches on name or qualified name case-insensitively; filter
	// for the exact qualified name.
	els, err := st.FindExact(qn)
	if err != nil {
		return store.Element{}, false, err
	}
	for _, el := range els {
		if strings.EqualFold(el.QualifiedName, qn) {
			return el, true, nil
		}
	}
	return store.Element{}, false, nil
}

// ExpandContext expands an ontology node to related code context over DB
// relationships, recursing up to depth hops (parity with
// expand_ontology_context).
func ExpandContext(st store.Backend, gid string, depth int) ([]store.Element, []store.Relationship, error) {
	return expandContext(st, gid, depth, map[string]bool{gid: true})
}

func expandContext(st store.Backend, gid string, depth int, visited map[string]bool) ([]store.Element, []store.Relationship, error) {
	var elements []store.Element
	var rels []store.Relationship

	// Outgoing edges only (parity with expand_ontology_context, whose
	// Datalog rule binds source_qualified = $gid).
	outgoing, err := st.Outgoing(gid)
	if err != nil {
		return nil, nil, err
	}

	for _, rel := range outgoing {
		target := rel.Target
		if target == "" || visited[target] {
			continue
		}
		visited[target] = true

		if el, ok, err := findElementByQN(st, target); err == nil && ok {
			elements = append(elements, el)
		}

		rels = append(rels, store.Relationship{
			Source:     gid,
			Target:     target,
			RelType:    rel.RelType,
			Confidence: rel.Confidence,
		})

		if depth > 1 {
			subEls, subRels, err := expandContext(st, target, depth-1, visited)
			if err != nil {
				return nil, nil, err
			}
			elements = append(elements, subEls...)
			rels = append(rels, subRels...)
		}
	}
	return elements, rels, nil
}

// GetContext assembles the full ontology context for a semantic query
// (parity with get_ontology_context): matched nodes, their expanded
// neighborhoods, code_refs resolution for concept-layer nodes, and
// workflow / step / failure-mode payloads for procedural matches.
func GetContext(st store.Backend, query string, depth int) (ContextResult, error) {
	matched, err := SearchOntologyNodes(st, query)
	if err != nil {
		return ContextResult{}, err
	}
	if len(matched) == 0 {
		return ContextResult{}, nil
	}

	res := ContextResult{MatchedNodes: matched}
	var sum float64
	for _, node := range matched {
		res.MatchReasons = append(res.MatchReasons, node.MatchReason)
		sum += node.MatchScore

		els, rels, err := ExpandContext(st, node.GID, depth)
		if err != nil {
			return ContextResult{}, err
		}
		res.ExpandedCodeContext = append(res.ExpandedCodeContext, els...)
		res.ExpandedRelations = append(res.ExpandedRelations, rels...)

		if !IsProceduralType(node.ElementType) {
			// Concept-layer node: resolve code_refs metadata into actual
			// indexed code elements (same logic as ConceptSearch).
			el, ok, err := findElementByQN(st, node.GID)
			if err != nil {
				return ContextResult{}, err
			}
			if ok {
				codeRefs := jsonStrArray(el.Metadata, "code_refs")
				if len(codeRefs) > 0 {
					resolved, err := ResolveCodeRefs(st, codeRefs, depth*20)
					if err != nil {
						return ContextResult{}, err
					}
					res.ExpandedCodeContext = append(res.ExpandedCodeContext, resolved...)
				}
			}
		}

		switch node.ElementType {
		case TypeWorkflow:
			if el, ok, _ := findElementByQN(st, node.GID); ok {
				if w, err := workflowFromElement(el); err == nil {
					res.Workflows = append(res.Workflows, w)
				}
			}
		case TypeWorkflowStep:
			if el, ok, _ := findElementByQN(st, node.GID); ok {
				if s, err := workflowStepFromElement(el); err == nil {
					res.WorkflowSteps = append(res.WorkflowSteps, s)
				}
			}
		case TypeFailureMode:
			if el, ok, _ := findElementByQN(st, node.GID); ok {
				if f, err := failureModeFromElement(el); err == nil {
					res.FailureModes = append(res.FailureModes, f)
				}
			}
		}
	}
	res.Confidence = sum / float64(len(matched))
	return res, nil
}

// Trace finds a workflow by name/alias/GID (or by a step's name, tracing
// its parent workflow) and returns its ordered steps — the
// kg_trace_workflow behavior (parity with trace_workflow).
func Trace(st store.Backend, workflowQuery string) ([]WorkflowStepNode, error) {
	workflowGID := ""
	workflow, err := findWorkflow(st, workflowQuery)
	if err != nil {
		return nil, err
	}
	if workflow != nil {
		workflowGID = workflow.GID
	} else {
		// Fallback: match a workflow_step by name/alias and trace its parent.
		nodes, err := SearchOntologyNodes(st, workflowQuery)
		if err != nil {
			return nil, err
		}
		for _, n := range nodes {
			if n.ElementType != TypeWorkflowStep {
				continue
			}
			el, ok, err := findElementByQN(st, n.GID)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, nil
			}
			if el.ParentQualified != "" {
				workflowGID = el.ParentQualified
			} else if wgid, _ := el.Metadata["workflow_gid"].(string); wgid != "" {
				workflowGID = wgid
			} else {
				return nil, nil
			}
			break
		}
	}
	if workflowGID == "" {
		return nil, nil
	}

	els, err := ontologyElements(st)
	if err != nil {
		return nil, err
	}
	steps := make([]WorkflowStepNode, 0)
	for _, el := range els {
		if el.ElementType != TypeWorkflowStep || el.ParentQualified != workflowGID {
			continue
		}
		step, err := workflowStepFromElement(el)
		if err != nil {
			continue
		}
		steps = append(steps, step)
	}
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].Order < steps[j].Order })
	return steps, nil
}

// findWorkflow searches workflow nodes by name / alias / GID substring
// (parity with search_workflows); first match wins.
func findWorkflow(st store.Backend, query string) (*WorkflowNode, error) {
	q := strings.ToLower(query)
	els, err := ontologyElements(st)
	if err != nil {
		return nil, err
	}
	for _, el := range els {
		if el.ElementType != TypeWorkflow {
			continue
		}
		aliases := jsonStrArray(el.Metadata, "aliases")
		description, _ := el.Metadata["description"].(string)
		nameMatch := strings.Contains(strings.ToLower(el.Name), q)
		aliasMatch := false
		for _, a := range aliases {
			if strings.Contains(strings.ToLower(a), q) {
				aliasMatch = true
				break
			}
		}
		gidMatch := strings.Contains(strings.ToLower(el.QualifiedName), q)
		if nameMatch || aliasMatch || gidMatch {
			node := workflowFromElementAliases(el, aliases, description)
			return &node, nil
		}
	}
	return nil, nil
}

func workflowFromElement(el store.Element) (WorkflowNode, error) {
	aliases := jsonStrArray(el.Metadata, "aliases")
	description, _ := el.Metadata["description"].(string)
	return workflowFromElementAliases(el, aliases, description), nil
}

func workflowFromElementAliases(el store.Element, aliases []string, description string) WorkflowNode {
	var meta WorkflowMetadata
	meta.GID, _ = el.Metadata["gid"].(string)
	meta.Ontology, _ = el.Metadata["ontology"].(string)
	meta.OntologyLayer, _ = el.Metadata["ontology_layer"].(string)
	meta.Aliases = aliases
	meta.Description = description
	meta.EntryPoints = jsonStrArray(el.Metadata, "entry_points")
	if v, ok := el.Metadata["step_count"].(float64); ok {
		n := int(v)
		meta.StepCount = &n
	}
	return WorkflowNode{
		GID:         el.QualifiedName,
		Name:        el.Name,
		ElementType: el.ElementType,
		Aliases:     aliases,
		Description: description,
		Metadata:    meta,
	}
}

func workflowStepFromElement(el store.Element) (WorkflowStepNode, error) {
	aliases := jsonStrArray(el.Metadata, "aliases")
	description, _ := el.Metadata["description"].(string)
	workflowGid, _ := el.Metadata["workflow_gid"].(string)
	order := 0
	if v, ok := el.Metadata["order"].(float64); ok {
		order = int(v)
	}
	meta := WorkflowStepMetadata{
		GID:           el.QualifiedName,
		Ontology:      "procedural",
		OntologyLayer: "procedural",
		WorkflowGid:   workflowGid,
		Order:         order,
		Aliases:       aliases,
		Description:   description,
		CodeRefs:      jsonStrArray(el.Metadata, "code_refs"),
		FailureModes:  jsonStrArray(el.Metadata, "failure_modes"),
		FeatureIDs:    jsonStrArray(el.Metadata, "feature_ids"),
		UserStoryIDs:  jsonStrArray(el.Metadata, "user_story_ids"),
	}
	return WorkflowStepNode{
		GID:         el.QualifiedName,
		Name:        el.Name,
		ElementType: el.ElementType,
		WorkflowGid: workflowGid,
		Order:       order,
		Description: description,
		Metadata:    meta,
	}, nil
}

func failureModeFromElement(el store.Element) (FailureModeNode, error) {
	aliases := jsonStrArray(el.Metadata, "aliases")
	description, _ := el.Metadata["description"].(string)
	meta := FailureModeMetadata{
		GID:           el.QualifiedName,
		Ontology:      "procedural",
		OntologyLayer: "procedural",
		Aliases:       aliases,
		Description:   description,
		HandledBy:     jsonStrArray(el.Metadata, "handled_by"),
	}
	return FailureModeNode{
		GID:         el.QualifiedName,
		Name:        el.Name,
		ElementType: el.ElementType,
		Description: description,
		Metadata:    meta,
	}, nil
}

// ConceptSearch implements the concept-gated search workflow (parity with
// concept_search): extract keywords, scan the concept ontology with each
// probe (full query first, then keywords), keep the best score per GID,
// then resolve all matched concepts' code_refs against indexed elements.
// With no concept hit, a name-based code search is returned as fallback.
func ConceptSearch(st store.Backend, rawInput string, limit int) (ConceptSearchResult, error) {
	keywords := ExtractKeywords(rawInput)
	if limit == 0 {
		limit = 20
	}

	probes := make([]string, 0, len(keywords)+1)
	if full := strings.ToLower(strings.TrimSpace(rawInput)); full != "" {
		probes = append(probes, full)
	}
	for _, kw := range keywords {
		if !containsStr(probes, kw) {
			probes = append(probes, kw)
		}
	}

	// Scan with each probe; keep the best score per gid, concept layer only.
	best := map[string]NodeInfo{}
	for _, probe := range probes {
		nodes, err := SearchOntologyNodes(st, probe)
		if err != nil {
			return ConceptSearchResult{}, err
		}
		for _, node := range nodes {
			if IsProceduralType(node.ElementType) {
				continue
			}
			if prev, ok := best[node.GID]; !ok || node.MatchScore > prev.MatchScore {
				best[node.GID] = node
			}
		}
	}
	matched := make([]NodeInfo, 0, len(best))
	for _, n := range best {
		matched = append(matched, n)
	}
	sort.SliceStable(matched, func(i, j int) bool {
		return matched[i].MatchScore > matched[j].MatchScore
	})
	if len(matched) > limit {
		matched = matched[:limit]
	}

	// Load full concept metadata (code_refs, docs, owned_by) per match.
	var allCodeRefs []string
	matchedConcepts := make([]MatchedConcept, 0, len(matched))
	for _, node := range matched {
		var codeRefs, docs, ownedBy []string
		if el, ok, err := findElementByQN(st, node.GID); err == nil && ok {
			codeRefs = jsonStrArray(el.Metadata, "code_refs")
			docs = jsonStrArray(el.Metadata, "docs")
			ownedBy = jsonStrArray(el.Metadata, "owned_by")
		}
		for _, r := range codeRefs {
			if !containsStr(allCodeRefs, r) {
				allCodeRefs = append(allCodeRefs, r)
			}
		}
		matchedConcepts = append(matchedConcepts, MatchedConcept{
			GID:         node.GID,
			Name:        node.Name,
			ElementType: node.ElementType,
			Description: node.Description,
			Aliases:     node.Aliases,
			MatchScore:  node.MatchScore,
			MatchReason: node.MatchReason,
			CodeRefs:    codeRefs,
			Docs:        docs,
			OwnedBy:     ownedBy,
		})
	}

	var linked []store.Element
	if len(allCodeRefs) > 0 {
		var err error
		linked, err = ResolveCodeRefs(st, allCodeRefs, 200)
		if err != nil {
			return ConceptSearchResult{}, err
		}
	}

	fallbackUsed := len(matched) == 0
	var fallback []store.Element
	if fallbackUsed {
		var err error
		fallback, err = searchCodeElementsByName(st, rawInput, 20)
		if err != nil {
			return ConceptSearchResult{}, err
		}
	}

	return ConceptSearchResult{
		Query:             rawInput,
		ExtractedKeywords: keywords,
		MatchedConcepts:   matchedConcepts,
		LinkedCode:        linked,
		ConceptMatchCount: len(matched),
		CodeRefCount:      len(allCodeRefs),
		LinkedCodeCount:   len(linked),
		FallbackUsed:      fallbackUsed,
		FallbackResults:   fallback,
	}, nil
}

// ResolveCodeRefs resolves code references (file paths, directory paths, or
// file::symbol references) against indexed code elements (parity with
// resolve_code_refs). Bounded by limit; never a full-table scan.
func ResolveCodeRefs(st store.Backend, codeRefs []string, limit int) ([]store.Element, error) {
	if len(codeRefs) == 0 || limit == 0 {
		return nil, nil
	}
	els, err := st.Elements()
	if err != nil {
		return nil, err
	}

	var matched []store.Element
	seen := map[string]bool{}
	for _, rawRef := range codeRefs {
		if len(matched) >= limit {
			break
		}
		r := NormalizePath(rawRef)
		if r == "" {
			continue
		}

		// Exact qualified_name hit (file::symbol already stored as QN).
		if el, ok := findByQN(els, rawRef); ok {
			if !seen[el.QualifiedName] {
				seen[el.QualifiedName] = true
				matched = append(matched, el)
			}
			continue
		}
		if el, ok := findByQN(els, r); ok {
			if !seen[el.QualifiedName] {
				seen[el.QualifiedName] = true
				matched = append(matched, el)
			}
			continue
		}

		// file::symbol form: match file (prefix, either direction) and symbol.
		if file, sym, found := strings.Cut(r, "::"); found {
			fileNorm := NormalizePath(file)
			symLower := strings.ToLower(sym)
			for _, el := range els {
				if len(matched) >= limit {
					break
				}
				efile := NormalizePath(el.FilePath)
				matchesFile := efile == fileNorm || strings.HasSuffix(efile, fileNorm) || strings.HasSuffix(fileNorm, efile)
				matchesSym := strings.EqualFold(el.Name, sym) ||
					strings.HasSuffix(strings.ToLower(el.QualifiedName), "::"+symLower) ||
					strings.Contains(strings.ToLower(el.Name), symLower)
				if matchesFile && matchesSym && !seen[el.QualifiedName] {
					seen[el.QualifiedName] = true
					matched = append(matched, el)
				}
			}
			if len(matched) < limit {
				// Symbol-only name search restricted to the file.
				for _, el := range els {
					if len(matched) >= limit {
						break
					}
					efile := NormalizePath(el.FilePath)
					if (efile == fileNorm || strings.HasSuffix(efile, fileNorm) || strings.HasSuffix(fileNorm, efile)) &&
						!seen[el.QualifiedName] &&
						strings.Contains(strings.ToLower(el.Name), symLower) {
						seen[el.QualifiedName] = true
						matched = append(matched, el)
					}
				}
			}
			continue
		}

		// file or directory form: prefix match on path, either direction.
		for _, el := range els {
			if len(matched) >= limit {
				break
			}
			efile := NormalizePath(el.FilePath)
			if efile == r || strings.HasSuffix(efile, r) || strings.HasSuffix(r, efile) {
				if !seen[el.QualifiedName] {
					seen[el.QualifiedName] = true
					matched = append(matched, el)
				}
			}
		}
	}
	return matched, nil
}

// searchByName is the typed name search the Rust engine implements as a
// regex substring over the lowercased element name (search_by_name_typed).
// excludeOntology drops ontology:// rows for code-only callers.
//
// ponytail: one full element scan per probe, capped at limit. The Rust
// engine had the same scan complexity (:limit still walks the relation).
// Upgrade path: a Backend method backed by an indexed LIKE/substring query.
func searchByName(st store.Backend, name string, limit int, excludeOntology bool) ([]store.Element, error) {
	if limit < 1 {
		limit = 1
	}
	els, err := st.Elements()
	if err != nil {
		return nil, err
	}
	nameLower := strings.ToLower(name)
	if nameLower == "" {
		return nil, nil
	}
	out := make([]store.Element, 0, limit)
	for _, el := range els {
		if excludeOntology && IsOntologyFilePath(el.FilePath) {
			continue
		}
		if strings.Contains(strings.ToLower(el.Name), nameLower) {
			out = append(out, el)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

// searchCodeElementsByName is the name-based fallback when no concept
// matched (parity with search_code_elements_by_name). Only non-ontology
// elements are considered.
func searchCodeElementsByName(st store.Backend, name string, limit int) ([]store.Element, error) {
	return searchByName(st, name, limit, true)
}

// OntologyStatus computes counts by type over the stored ontology layer
// (parity with get_ontology_status).
func OntologyStatus(st store.Backend) (Status, error) {
	els, err := ontologyElements(st)
	if err != nil {
		return Status{}, err
	}
	status := Status{
		ConceptCounts:    map[string]int{},
		ProceduralCounts: map[string]int{},
	}
	workflowGIDs := make([]string, 0)
	withFailureModes := map[string]bool{}
	for _, el := range els {
		aliases := jsonStrArray(el.Metadata, "aliases")
		status.TotalAliases += len(aliases)
		if len(aliases) == 0 {
			status.NodesMissingAliases++
		}
		if IsProceduralType(el.ElementType) {
			status.ProceduralCounts[el.ElementType]++
		} else {
			status.ConceptCounts[el.ElementType]++
		}

		source, _ := el.Metadata["source"].(string)
		if source == "dynamic" {
			if IsProceduralType(el.ElementType) {
				if el.ElementType == TypeWorkflow {
					status.DynamicWorkflows++
				}
			} else {
				status.DynamicConcepts++
			}
		}

		switch el.ElementType {
		case TypeWorkflow:
			workflowGIDs = append(workflowGIDs, el.QualifiedName)
		case TypeWorkflowStep:
			if len(jsonStrArray(el.Metadata, "failure_modes")) == 0 {
				continue
			}
			if wgid, ok := el.Metadata["workflow_gid"].(string); ok {
				withFailureModes[wgid] = true
			}
		}
	}
	for _, gid := range workflowGIDs {
		if !withFailureModes[gid] {
			status.WorkflowsWithoutFailureModes++
		}
	}
	return status, nil
}

// MatchScore calculates the match score for a (lowercased) query against an
// ontology node's name, aliases and description (parity with
// calculate_match_score; reasons are identical strings).
func MatchScore(query, name string, aliases []string, description string) (float64, string) {
	queryLower := strings.ToLower(query)
	nameLower := strings.ToLower(name)
	descLower := strings.ToLower(description)

	if nameLower == queryLower {
		return 1.0, fmt.Sprintf("exact name match: %s", name)
	}
	if strings.Contains(nameLower, queryLower) {
		return 0.8, fmt.Sprintf("name contains '%s': %s", query, name)
	}
	for _, alias := range aliases {
		aliasLower := strings.ToLower(alias)
		if aliasLower == queryLower {
			return 0.9, fmt.Sprintf("exact alias match: %s", alias)
		}
		if strings.Contains(aliasLower, queryLower) {
			return 0.7, fmt.Sprintf("alias contains '%s': %s", query, alias)
		}
	}
	if strings.Contains(descLower, queryLower) {
		return 0.5, fmt.Sprintf("description contains '%s'", query)
	}

	// Multi-word query: score based on the fraction of query words matched.
	words := strings.Fields(queryLower)
	if len(words) > 1 {
		var matchedWords int
		var sources []string
		for _, w := range words {
			if len(w) < 3 {
				continue
			}
			switch {
			case strings.Contains(nameLower, w):
				matchedWords++
				sources = append(sources, fmt.Sprintf("%s (name)", w))
			case aliasesContain(aliases, w):
				matchedWords++
				sources = append(sources, fmt.Sprintf("%s (alias)", w))
			case strings.Contains(descLower, w):
				matchedWords++
				sources = append(sources, fmt.Sprintf("%s (desc)", w))
			}
		}
		meaningful := 0
		for _, w := range words {
			if len(w) >= 3 {
				meaningful++
			}
		}
		if meaningful > 0 && matchedWords > 0 {
			ratio := float64(matchedWords) / float64(meaningful)
			if ratio >= 0.5 {
				return ratio * 0.7, fmt.Sprintf("%d of %d meaningful query words matched: %s",
					matchedWords, meaningful, strings.Join(sources, ", "))
			}
			if ratio > 0 {
				return ratio * 0.3, fmt.Sprintf("partial match: %d of %d words: %s",
					matchedWords, meaningful, strings.Join(sources, ", "))
			}
		}
	}

	// Note: the Rust reference's trailing single-word substring block is
	// unreachable (every containment case returned above); it is omitted
	// here rather than ported as dead code.
	return 0, ""
}

func aliasesContain(aliases []string, w string) bool {
	for _, a := range aliases {
		if strings.Contains(strings.ToLower(a), w) {
			return true
		}
	}
	return false
}

// ExtractKeywords tokenizes raw user input, lowercases it, strips
// punctuation, drops stop words and very short tokens, and dedupes
// order-preserving (parity with extract_keywords).
func ExtractKeywords(raw string) []string {
	var stopwords = map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true, "but": true,
		"of": true, "to": true, "in": true, "on": true, "for": true, "with": true,
		"is": true, "are": true, "was": true, "were": true, "be": true, "been": true,
		"being": true, "this": true, "that": true, "these": true, "those": true,
		"it": true, "its": true, "as": true, "at": true, "by": true, "from": true,
		"how": true, "what": true, "where": true, "why": true, "when": true,
		"which": true, "who": true, "whom": true, "whose": true, "do": true,
		"does": true, "did": true, "can": true, "could": true, "should": true,
		"would": true, "will": true, "shall": true, "may": true, "might": true,
		"must": true, "have": true, "has": true, "had": true, "i": true,
		"we": true, "you": true, "they": true, "he": true, "she": true,
		"my": true, "our": true, "your": true, "their": true, "me": true,
		"us": true, "them": true, "about": true, "into": true, "than": true,
		"then": true, "so": true, "if": true, "no": true, "not": true,
		"any": true, "all": true, "find": true, "show": true, "get": true,
		"tell": true, "explain": true, "describe": true, "see": true,
		"look": true, "want": true, "need": true, "please": true, "help": true,
		"use": true, "using": true, "used": true, "like": true, "also": true,
	}

	var out []string
	seen := map[string]bool{}
	for _, w := range strings.Fields(raw) {
		w = strings.ToLower(trimPunct(w))
		if len(w) < 2 || stopwords[w] {
			continue
		}
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

// trimPunct strips leading/trailing characters that are neither
// alphanumeric nor '-' or '_' (parity with trim_matches).
func trimPunct(w string) string {
	return strings.TrimFunc(w, func(c rune) bool {
		if c == '-' || c == '_' {
			return false
		}
		return !unicode.IsLetter(c) && !unicode.IsDigit(c)
	})
}

// NormalizePath normalizes a path-like reference: trim whitespace, strip a
// leading "./", and trim surrounding slashes (parity with normalize_path).
func NormalizePath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "./")
	return strings.Trim(p, "/")
}

// SelfTest runs a non-mutating probe over each ontology query path with the
// synthetic "__selftest__" query, which matches nothing (parity with
// self_test minus the Cozo schema snapshots, which have no Go equivalent —
// the Go store enforces its schema at migration time).
type SelfTestEntry struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type SelfTestReport struct {
	KgContext        SelfTestEntry `json:"kg_context"`
	KgConceptMap     SelfTestEntry `json:"kg_concept_map"`
	KgTraceWorkflow  SelfTestEntry `json:"kg_trace_workflow"`
	KgOntologyStatus SelfTestEntry `json:"kg_ontology_status"`
	AllOK            bool          `json:"all_ok"`
}

// SelfTest probes each kg_* query path; ok=true means the call completed
// without error.
func SelfTest(st store.Backend) SelfTestReport {
	probe := "__selftest__"

	var report SelfTestReport
	if _, err := GetContext(st, probe, 1); err != nil {
		report.KgContext = SelfTestEntry{Error: err.Error()}
	} else {
		report.KgContext = SelfTestEntry{OK: true}
	}
	if _, err := SearchOntologyNodes(st, probe); err != nil {
		report.KgConceptMap = SelfTestEntry{Error: err.Error()}
	} else {
		report.KgConceptMap = SelfTestEntry{OK: true}
	}
	if _, err := Trace(st, probe); err != nil {
		report.KgTraceWorkflow = SelfTestEntry{Error: err.Error()}
	} else {
		report.KgTraceWorkflow = SelfTestEntry{OK: true}
	}
	if _, err := OntologyStatus(st); err != nil {
		report.KgOntologyStatus = SelfTestEntry{Error: err.Error()}
	} else {
		report.KgOntologyStatus = SelfTestEntry{OK: true}
	}
	report.AllOK = report.KgContext.OK && report.KgConceptMap.OK &&
		report.KgTraceWorkflow.OK && report.KgOntologyStatus.OK
	return report
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// findByQN finds one element by qualified name within a scanned set.
func findByQN(els []store.Element, qn string) (store.Element, bool) {
	for _, el := range els {
		if strings.EqualFold(el.QualifiedName, strings.TrimSpace(qn)) {
			return el, true
		}
	}
	return store.Element{}, false
}
