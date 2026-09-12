package ontology

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// Procedural element types for the procedural layer (parity with
// ProceduralElementType in the Rust reference).
const (
	TypeWorkflow      = "workflow"
	TypeWorkflowStep  = "workflow_step"
	TypeDecisionPoint = "decision_point"
	TypeFailureMode   = "failure_mode"
	TypePlaybookStep  = "playbook_step"
)

// Concept (domain layer) element types (parity with ConceptElementType).
const (
	TypeDomainEntity  = "domain_entity"
	TypeService       = "service"
	TypeAPIEndpoint   = "api_endpoint"
	TypeDataStore     = "data_store"
	TypeEnvironment   = "environment"
	TypeKnownIssue    = "known_issue"
	TypePlaybook      = "playbook"
	TypeTeamKnowledge = "team_knowledge"
)

// Procedural relationship types (parity with ProceduralRelationshipType).
const (
	RelHasStep           = "has_step"
	RelNextStep          = "next_step"
	RelBranchesTo        = "branches_to"
	RelImplementedBy     = "implemented_by"
	RelHasFailureMode    = "has_failure_mode"
	RelHandledByPlaybook = "handled_by_playbook"
)

// Concept relationship types (parity with ConceptRelationshipType).
const (
	RelOwnsConcept        = "owns_concept"
	RelImplementsConcept  = "implements_concept"
	RelExposesEndpoint    = "exposes_endpoint"
	RelReadsFrom          = "reads_from"
	RelWritesTo           = "writes_to"
	RelDocumentsConcept   = "documents_concept"
	RelHasKnownIssue      = "has_known_issue"
	RelResolvedByPlaybook = "resolved_by_playbook"
)

// ProceduralTypes is the set of procedural-layer element types.
var ProceduralTypes = map[string]bool{
	TypeWorkflow: true, TypeWorkflowStep: true, TypeDecisionPoint: true,
	TypeFailureMode: true, TypePlaybookStep: true,
}

// ConceptTypes is the set of domain-layer element types.
var ConceptTypes = map[string]bool{
	TypeDomainEntity: true, TypeService: true, TypeAPIEndpoint: true,
	TypeDataStore: true, TypeEnvironment: true, TypeKnownIssue: true,
	TypePlaybook: true, TypeTeamKnowledge: true,
}

// OntologyElements is every element type the ontology layer owns.
var OntologyElements = map[string]bool{
	TypeDomainEntity: true, TypeService: true, TypeAPIEndpoint: true,
	TypeDataStore: true, TypeEnvironment: true, TypeKnownIssue: true,
	TypePlaybook: true, TypeTeamKnowledge: true,
	TypeWorkflow: true, TypeWorkflowStep: true, TypeDecisionPoint: true,
	TypeFailureMode: true, TypePlaybookStep: true,
}

// IsProceduralType reports whether t belongs to the procedural layer.
func IsProceduralType(t string) bool { return ProceduralTypes[t] }

// GID is the stable global id of an ontology node,
// "env:scope:ontology_type:id:version" (parity with OntologyGid).
type GID struct {
	Env          string
	Scope        string
	OntologyType string
	ID           string
	Version      string
}

// ParseGid parses a GID string like "local:checkout-service:domain_entity:refund:v1".
func ParseGid(s string) (GID, bool) {
	parts := strings.Split(s, ":")
	if len(parts) != 5 {
		return GID{}, false
	}
	return GID{Env: parts[0], Scope: parts[1], OntologyType: parts[2], ID: parts[3], Version: parts[4]}, true
}

// Format renders the canonical "env:scope:type:id:version" form.
func (g GID) Format() string {
	return fmt.Sprintf("%s:%s:%s:%s:%s", g.Env, g.Scope, g.OntologyType, g.ID, g.Version)
}

// NewGid builds a version-1 GID.
func NewGid(env, scope, ontologyType, id string) GID {
	return GID{Env: env, Scope: scope, OntologyType: ontologyType, ID: id, Version: "v1"}
}

// NormalizeAlias normalizes an alias: lowercase, trimmed, keeping only
// alphanumeric characters, spaces, hyphens and underscores (parity with
// normalize_alias).
func NormalizeAlias(alias string) string {
	alias = strings.ToLower(alias)
	alias = strings.TrimSpace(alias)
	var b strings.Builder
	for _, c := range alias {
		if isAliasRune(c) {
			b.WriteRune(c)
		}
	}
	return b.String()
}

func isAliasRune(c rune) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		return true
	case c == ' ' || c == '-' || c == '_':
		return true
	default:
		return unicode.IsLetter(c) || unicode.IsDigit(c)
	}
}

// NormalizeAliases normalizes each alias in turn.
func NormalizeAliases(aliases []string) []string {
	out := make([]string, len(aliases))
	for i, a := range aliases {
		out[i] = NormalizeAlias(a)
	}
	return out
}

// ConceptMetadata is the metadata contract for concept ontology nodes.
// JSON tags mirror the Rust serde field names so stored metadata stays
// byte-comparable with the Rust engine's.
type ConceptMetadata struct {
	GID           string   `json:"gid"`
	Ontology      string   `json:"ontology"`
	OntologyLayer string   `json:"ontology_layer"`
	Aliases       []string `json:"aliases,omitempty"`
	Description   string   `json:"description"`
	Source        *string  `json:"source,omitempty"`
	ValidFrom     *string  `json:"valid_from,omitempty"`
	ValidUntil    *string  `json:"valid_until,omitempty"`
	OwnedBy       []string `json:"owned_by,omitempty"`
	CodeRefs      []string `json:"code_refs,omitempty"`
	Docs          []string `json:"docs,omitempty"`
	Stale         bool     `json:"stale"`
	StaleReason   *string  `json:"stale_reason,omitempty"`
	LastSeenAt    *string  `json:"last_seen_at,omitempty"`
}

// ConceptNode is a concept-layer node for graph insertion.
type ConceptNode struct {
	GID         string          `json:"gid"`
	Name        string          `json:"name"`
	ElementType string          `json:"element_type"`
	Aliases     []string        `json:"aliases"`
	Description string          `json:"description"`
	Env         string          `json:"env"`
	Metadata    ConceptMetadata `json:"metadata"`
}

// NewConceptNode builds a concept node with a name-derived seed alias
// (parity with ConceptMetadata::new).
func NewConceptNode(env, scope, elementType, id, name, description string) ConceptNode {
	g := NewGid(env, scope, elementType, id)
	seed := []string{NormalizeAlias(name)}
	return ConceptNode{
		GID:         g.Format(),
		Name:        name,
		ElementType: elementType,
		Aliases:     seed,
		Description: description,
		Env:         env,
		Metadata: ConceptMetadata{
			GID:           g.Format(),
			Ontology:      "concept",
			OntologyLayer: "domain",
			Aliases:       seed,
			Description:   description,
		},
	}
}

// WithAliases appends normalized aliases to the node (parity with with_aliases).
func (n *ConceptNode) WithAliases(aliases []string) {
	n.Aliases = append(n.Aliases, NormalizeAliases(aliases)...)
	n.Metadata.Aliases = n.Aliases
}

// WithSource records the YAML source path in the metadata.
func (n *ConceptNode) WithSource(source string) {
	n.Metadata.Source = &source
}

// WorkflowMetadata is the metadata contract for workflow nodes.
type WorkflowMetadata struct {
	GID           string   `json:"gid"`
	Ontology      string   `json:"ontology"`
	OntologyLayer string   `json:"ontology_layer"`
	Aliases       []string `json:"aliases,omitempty"`
	Description   string   `json:"description"`
	EntryPoints   []string `json:"entry_points,omitempty"`
	StepCount     *int     `json:"step_count,omitempty"`
	Source        *string  `json:"source,omitempty"`
	ValidFrom     *string  `json:"valid_from,omitempty"`
	ValidUntil    *string  `json:"valid_until,omitempty"`
	Stale         bool     `json:"stale"`
	StaleReason   *string  `json:"stale_reason,omitempty"`
	LastSeenAt    *string  `json:"last_seen_at,omitempty"`
}

// WorkflowNode is a workflow node for graph insertion.
type WorkflowNode struct {
	GID         string           `json:"gid"`
	Name        string           `json:"name"`
	ElementType string           `json:"element_type"`
	Aliases     []string         `json:"aliases"`
	Description string           `json:"description"`
	Env         string           `json:"env"`
	Metadata    WorkflowMetadata `json:"metadata"`
}

// NewWorkflowNode builds a workflow node (parity with WorkflowNode::new).
func NewWorkflowNode(env, scope, workflowID, name, description string) WorkflowNode {
	g := NewGid(env, scope, TypeWorkflow, workflowID).Format()
	seed := []string{NormalizeAlias(name)}
	return WorkflowNode{
		GID:         g,
		Name:        name,
		ElementType: TypeWorkflow,
		Aliases:     seed,
		Description: description,
		Env:         env,
		Metadata: WorkflowMetadata{
			GID:           g,
			Ontology:      "procedural",
			OntologyLayer: "procedural",
			Aliases:       seed,
			Description:   description,
		},
	}
}

// WorkflowStepMetadata is the metadata contract for workflow step nodes.
type WorkflowStepMetadata struct {
	GID           string   `json:"gid"`
	Ontology      string   `json:"ontology"`
	OntologyLayer string   `json:"ontology_layer"`
	WorkflowGid   string   `json:"workflow_gid"`
	Order         int      `json:"order"`
	Aliases       []string `json:"aliases,omitempty"`
	Description   string   `json:"description"`
	CodeRefs      []string `json:"code_refs,omitempty"`
	FailureModes  []string `json:"failure_modes,omitempty"`
	FeatureIDs    []string `json:"feature_ids,omitempty"`
	UserStoryIDs  []string `json:"user_story_ids,omitempty"`
	Source        *string  `json:"source,omitempty"`
	Stale         bool     `json:"stale"`
	StaleReason   *string  `json:"stale_reason,omitempty"`
	LastSeenAt    *string  `json:"last_seen_at,omitempty"`
}

// WorkflowStepNode is a workflow step node for graph insertion.
type WorkflowStepNode struct {
	GID         string               `json:"gid"`
	Name        string               `json:"name"`
	ElementType string               `json:"element_type"`
	Aliases     []string             `json:"aliases"`
	WorkflowGid string               `json:"workflow_gid"`
	Order       int                  `json:"order"`
	Description string               `json:"description"`
	Env         string               `json:"env"`
	Metadata    WorkflowStepMetadata `json:"metadata"`
}

// NewWorkflowStepNode builds a step node with a name-derived seed alias
// (parity with WorkflowStepNode::new).
func NewWorkflowStepNode(env, scope, workflowID, stepID, name string, order int, description string) WorkflowStepNode {
	g := NewGid(env, scope, TypeWorkflowStep, stepID).Format()
	wg := NewGid(env, scope, TypeWorkflow, workflowID).Format()
	seed := []string{NormalizeAlias(name)}
	return WorkflowStepNode{
		GID:         g,
		Name:        name,
		ElementType: TypeWorkflowStep,
		Aliases:     seed,
		WorkflowGid: wg,
		Order:       order,
		Description: description,
		Env:         env,
		Metadata: WorkflowStepMetadata{
			GID:           g,
			Ontology:      "procedural",
			OntologyLayer: "procedural",
			WorkflowGid:   wg,
			Order:         order,
			Aliases:       seed,
			Description:   description,
		},
	}
}

// WithAliases appends normalized aliases to the workflow node (parity with
// with_aliases).
func (n *WorkflowNode) WithAliases(aliases []string) {
	n.Metadata.Aliases = append(n.Metadata.Aliases, NormalizeAliases(aliases)...)
	n.Aliases = n.Metadata.Aliases
}

// WithAliases merges YAML-declared aliases on top of the name-derived seed
// (FR-HEA-01 parity: previously parsed but never applied in the Rust loader).
func (n *WorkflowStepNode) WithAliases(aliases []string) {
	n.Metadata.Aliases = append(n.Metadata.Aliases, NormalizeAliases(aliases)...)
	n.Aliases = n.Metadata.Aliases
}

// FailureModeMetadata is the metadata contract for failure mode nodes.
type FailureModeMetadata struct {
	GID           string   `json:"gid"`
	Ontology      string   `json:"ontology"`
	OntologyLayer string   `json:"ontology_layer"`
	Aliases       []string `json:"aliases,omitempty"`
	Description   string   `json:"description"`
	HandledBy     []string `json:"handled_by,omitempty"`
	Source        *string  `json:"source,omitempty"`
	Stale         bool     `json:"stale"`
	StaleReason   *string  `json:"stale_reason,omitempty"`
	LastSeenAt    *string  `json:"last_seen_at,omitempty"`
}

// FailureModeNode is a failure mode node for graph insertion.
type FailureModeNode struct {
	GID         string              `json:"gid"`
	Name        string              `json:"name"`
	ElementType string              `json:"element_type"`
	Description string              `json:"description"`
	Env         string              `json:"env"`
	Metadata    FailureModeMetadata `json:"metadata"`
}

// NewFailureModeNode builds a failure mode node (parity with
// FailureModeNode::new).
func NewFailureModeNode(env, scope, failureID, name, description string) FailureModeNode {
	g := NewGid(env, scope, TypeFailureMode, failureID).Format()
	seed := []string{NormalizeAlias(name)}
	return FailureModeNode{
		GID:         g,
		Name:        name,
		ElementType: TypeFailureMode,
		Description: description,
		Env:         env,
		Metadata: FailureModeMetadata{
			GID:           g,
			Ontology:      "procedural",
			OntologyLayer: "procedural",
			Aliases:       seed,
			Description:   description,
		},
	}
}

// elementFilePrefix marks ontology rows in code_elements.file_path, exactly
// like the Rust engine ("ontology://<gid>").
const elementFilePrefix = "ontology://"

// IsOntologyFilePath reports whether a stored element file_path belongs to
// the ontology layer.
func IsOntologyFilePath(path string) bool {
	return strings.HasPrefix(path, elementFilePrefix)
}

// toMap converts a metadata struct into the map form store.Element carries.
func toMap(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]any{}
	}
	return m
}

// jsonStrArray reads a []string field out of a metadata map, tolerating a
// missing or non-array field (parity with json_str_array).
func jsonStrArray(meta map[string]any, key string) []string {
	raw, ok := meta[key]
	if !ok {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
