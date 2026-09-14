package ontology

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/FreePeak/LeanKG/go/internal/store"
	"gopkg.in/yaml.v3"
)

// YAML schema (parity with loader.rs): concepts.yaml, workflows.yaml,
// aliases.yaml under the ontology directory.

type conceptsYAML struct {
	Concepts []conceptDef `yaml:"concepts"`
}

type conceptDef struct {
	ID          string   `yaml:"id"`
	Type        string   `yaml:"type"`
	Name        string   `yaml:"name"`
	Env         string   `yaml:"env"`
	Aliases     []string `yaml:"aliases"`
	Description string   `yaml:"description"`
	OwnedBy     []string `yaml:"owned_by"`
	CodeRefs    []string `yaml:"code_refs"`
	Docs        []string `yaml:"docs"`
}

type workflowsYAML struct {
	Workflows []workflowDef `yaml:"workflows"`
}

type workflowDef struct {
	ID          string    `yaml:"id"`
	Name        string    `yaml:"name"`
	Env         string    `yaml:"env"`
	Aliases     []string  `yaml:"aliases"`
	Description string    `yaml:"description"`
	EntryPoints []string  `yaml:"entry_points"`
	Steps       []stepDef `yaml:"steps"`
}

type stepDef struct {
	ID           string   `yaml:"id"`
	Name         string   `yaml:"name"`
	CodeRefs     []string `yaml:"code_refs"`
	FailureModes []string `yaml:"failure_modes"`
	Aliases      []string `yaml:"aliases"`
	FeatureIDs   []string `yaml:"feature_ids"`
	UserStoryIDs []string `yaml:"user_story_ids"`
}

type aliasesYAML struct {
	Aliases []aliasDef `yaml:"aliases"`
}

type aliasDef struct {
	GID   string `yaml:"gid"`
	Alias string `yaml:"alias"`
}

// WorkflowSet is the result of loading workflows.yaml: node sets plus the
// procedural relationships that wire them together.
type WorkflowSet struct {
	Workflows     []WorkflowNode
	Steps         []WorkflowStepNode
	FailureModes  []FailureModeNode
	Relationships []store.Relationship
}

// LoadWorkflowsYAML parses and validates a workflows.yaml file.
//
// Validation (stricter than the Rust serde-only load, deliberate): workflow
// and step ids/names must be non-empty, workflow ids must be unique within
// the file, and step ids must be unique within their workflow.
func LoadWorkflowsYAML(path string) (*WorkflowSet, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc workflowsYAML
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}

	set := &WorkflowSet{}
	workflowIDs := make(map[string]bool, len(doc.Workflows))
	for _, wf := range doc.Workflows {
		if wf.ID == "" {
			return nil, fmt.Errorf("%s: workflow with empty id", path)
		}
		if wf.Name == "" {
			return nil, fmt.Errorf("%s: workflow %q has empty name", path, wf.ID)
		}
		if workflowIDs[wf.ID] {
			return nil, fmt.Errorf("%s: duplicate workflow id %q", path, wf.ID)
		}
		workflowIDs[wf.ID] = true

		env := wf.Env
		if env == "" {
			env = "local"
		}
		scope := "default"

		node := NewWorkflowNode(env, scope, wf.ID, wf.Name, wf.Description)
		node.WithAliases(wf.Aliases)
		stepCount := len(wf.Steps)
		node.Metadata.StepCount = &stepCount
		node.Metadata.EntryPoints = wf.EntryPoints
		node.Metadata.Source = strPtr(path)
		node.Aliases = node.Metadata.Aliases

		var prevStep *WorkflowStepNode
		stepIDs := make(map[string]bool, len(wf.Steps))
		for i, sd := range wf.Steps {
			if sd.ID == "" {
				return nil, fmt.Errorf("%s: workflow %q step %d has empty id", path, wf.ID, i+1)
			}
			if sd.Name == "" {
				return nil, fmt.Errorf("%s: workflow %q step %q has empty name", path, wf.ID, sd.ID)
			}
			if stepIDs[sd.ID] {
				return nil, fmt.Errorf("%s: workflow %q has duplicate step id %q", path, wf.ID, sd.ID)
			}
			stepIDs[sd.ID] = true

			step := NewWorkflowStepNode(env, scope, wf.ID, sd.ID, sd.Name, i+1, "")
			step.WithAliases(sd.Aliases)
			step.Metadata.CodeRefs = sd.CodeRefs
			step.Metadata.FailureModes = sd.FailureModes
			step.Metadata.FeatureIDs = sd.FeatureIDs
			step.Metadata.UserStoryIDs = sd.UserStoryIDs
			step.Aliases = step.Metadata.Aliases
			set.Steps = append(set.Steps, step)

			set.Relationships = append(set.Relationships, store.Relationship{
				Source:     node.GID,
				Target:     step.GID,
				RelType:    RelHasStep,
				Confidence: 1.0,
			})
			if prevStep != nil {
				set.Relationships = append(set.Relationships, store.Relationship{
					Source:     prevStep.GID,
					Target:     step.GID,
					RelType:    RelNextStep,
					Confidence: 1.0,
				})
			}

			for _, failureID := range sd.FailureModes {
				fm := NewFailureModeNode(env, scope, failureID, failureID,
					fmt.Sprintf("Failure mode: %s", failureID))
				set.FailureModes = append(set.FailureModes, fm)
				set.Relationships = append(set.Relationships, store.Relationship{
					Source:     step.GID,
					Target:     fm.GID,
					RelType:    RelHasFailureMode,
					Confidence: 1.0,
				})
			}

			prevStep = &step
		}

		set.Workflows = append(set.Workflows, node)
	}
	return set, nil
}

// LoadConceptsYAML parses a concepts.yaml file. Unknown concept types
// default to domain_entity (parity with the Rust warning path); scope is
// derived from the first owned_by entry, falling back to "default".
func LoadConceptsYAML(path string) ([]ConceptNode, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc conceptsYAML
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}

	nodes := make([]ConceptNode, 0, len(doc.Concepts))
	for _, cd := range doc.Concepts {
		if cd.ID == "" {
			return nil, fmt.Errorf("%s: concept with empty id", path)
		}
		elementType := cd.Type
		if !ConceptTypes[elementType] {
			elementType = TypeDomainEntity
		}
		env := cd.Env
		if env == "" {
			env = "local"
		}
		scope := "default"
		if len(cd.OwnedBy) > 0 && cd.OwnedBy[0] != "" {
			scope = cd.OwnedBy[0]
		}

		node := NewConceptNode(env, scope, elementType, cd.ID, cd.Name, cd.Description)
		node.WithAliases(cd.Aliases)
		node.WithSource(path)
		node.Metadata.OwnedBy = cd.OwnedBy
		node.Metadata.CodeRefs = cd.CodeRefs
		node.Metadata.Docs = cd.Docs
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// LoadAliasesYAML parses an aliases.yaml file into (gid, alias) pairs.
func LoadAliasesYAML(path string) ([][2]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc aliasesYAML
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	out := make([][2]string, 0, len(doc.Aliases))
	for _, a := range doc.Aliases {
		out = append(out, [2]string{a.GID, a.Alias})
	}
	return out, nil
}

func strPtr(s string) *string { return &s }

// Node → element conversion (parity with *_nodes_to_elements): each node
// becomes a code element keyed by its GID with an "ontology://" file path
// and language "ontology".

// Element renders the node as a store element.
func (n WorkflowNode) Element() store.Element {
	return store.Element{
		QualifiedName: n.GID,
		ElementType:   n.ElementType,
		Name:          n.Name,
		FilePath:      elementFilePrefix + n.GID,
		Language:      "ontology",
		Metadata:      toMap(n.Metadata),
	}
}

// Element renders the node as a store element. The parent is the workflow
// GID so trace_workflow-style parent lookups work over the graph.
func (n WorkflowStepNode) Element() store.Element {
	return store.Element{
		QualifiedName:   n.GID,
		ElementType:     n.ElementType,
		Name:            n.Name,
		FilePath:        elementFilePrefix + n.GID,
		Language:        "ontology",
		ParentQualified: n.WorkflowGid,
		Metadata:        toMap(n.Metadata),
	}
}

// Element renders the node as a store element.
func (n FailureModeNode) Element() store.Element {
	return store.Element{
		QualifiedName: n.GID,
		ElementType:   n.ElementType,
		Name:          n.Name,
		FilePath:      elementFilePrefix + n.GID,
		Language:      "ontology",
		Metadata:      toMap(n.Metadata),
	}
}

// Element renders the node as a store element.
func (n ConceptNode) Element() store.Element {
	return store.Element{
		QualifiedName: n.GID,
		ElementType:   n.ElementType,
		Name:          n.Name,
		FilePath:      elementFilePrefix + n.GID,
		Language:      "ontology",
		Metadata:      toMap(n.Metadata),
	}
}

// ConceptNodesToElements converts nodes to store elements.
func ConceptNodesToElements(nodes []ConceptNode) []store.Element {
	out := make([]store.Element, 0, len(nodes))
	for i := range nodes {
		out = append(out, nodes[i].Element())
	}
	return out
}

// WorkflowNodesToElements converts nodes to store elements.
func WorkflowNodesToElements(nodes []WorkflowNode) []store.Element {
	out := make([]store.Element, 0, len(nodes))
	for i := range nodes {
		out = append(out, nodes[i].Element())
	}
	return out
}

// WorkflowStepNodesToElements converts nodes to store elements.
func WorkflowStepNodesToElements(nodes []WorkflowStepNode) []store.Element {
	out := make([]store.Element, 0, len(nodes))
	for i := range nodes {
		out = append(out, nodes[i].Element())
	}
	return out
}

// FailureModeNodesToElements converts nodes to store elements.
func FailureModeNodesToElements(nodes []FailureModeNode) []store.Element {
	out := make([]store.Element, 0, len(nodes))
	for i := range nodes {
		out = append(out, nodes[i].Element())
	}
	return out
}
