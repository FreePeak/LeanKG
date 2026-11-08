package ontology

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// repoRoot is the LeanKG checkout root: this package is internal/ontology.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve test file location")
	}
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// refFile returns the file part of a code reference: "path" or "path::symbol".
func refFile(ref string) string {
	file, _, _ := strings.Cut(ref, "::")
	return file
}

// checkRepoRef fails when a code reference names a file or directory that
// is not in the repository. Symbol suffixes are not checked.
func checkRepoRef(t *testing.T, root, where, ref string) {
	t.Helper()
	file := refFile(ref)
	if file == "" {
		t.Errorf("%s: empty code reference %q", where, ref)
		return
	}
	if _, err := os.Stat(filepath.Join(root, file)); err != nil {
		t.Errorf("%s: code reference %q does not exist in the repo", where, ref)
	}
}

// TestRepoOntologyLoads loads the repository's own ontology/ directory through
// the production loaders and checks that every path it names is real.
func TestRepoOntologyLoads(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "ontology")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("repo ontology directory missing: want ontology/ at the repo root (stat err: %v)", err)
	}

	conceptsPath := filepath.Join(dir, "concepts.yaml")
	concepts, err := LoadConceptsYAML(conceptsPath)
	if err != nil {
		t.Fatalf("load concepts.yaml: %v", err)
	}
	workflows, err := LoadWorkflowsYAML(filepath.Join(dir, "workflows.yaml"))
	if err != nil {
		t.Fatalf("load workflows.yaml: %v", err)
	}
	if len(concepts) == 0 || len(workflows.Workflows) == 0 {
		t.Fatalf("want concepts and workflows, got %d concepts, %d workflows", len(concepts), len(workflows.Workflows))
	}

	// LoadConceptsYAML maps an unknown type to domain_entity without error,
	// so the raw type is checked against the valid set here.
	raw, err := os.ReadFile(conceptsPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc conceptsYAML
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	seenConcept := make(map[string]bool, len(doc.Concepts))
	for _, c := range doc.Concepts {
		where := "concept " + c.ID
		if c.ID == "" {
			t.Errorf("concept with empty id")
			continue
		}
		if seenConcept[c.ID] {
			t.Errorf("duplicate concept id %q", c.ID)
		}
		seenConcept[c.ID] = true
		if !ConceptTypes[c.Type] {
			t.Errorf("%s: type %q is not a valid concept type", where, c.Type)
		}
		for _, ref := range c.CodeRefs {
			checkRepoRef(t, root, where, ref)
		}
		for _, d := range c.Docs {
			checkRepoRef(t, root, where, d)
		}
	}

	rawWF, err := os.ReadFile(filepath.Join(dir, "workflows.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var wfDoc workflowsYAML
	if err := yaml.Unmarshal(rawWF, &wfDoc); err != nil {
		t.Fatal(err)
	}
	seenWorkflow := make(map[string]bool, len(wfDoc.Workflows))
	for _, wf := range wfDoc.Workflows {
		where := "workflow " + wf.ID
		if wf.ID == "" {
			t.Errorf("workflow with empty id")
		}
		if seenWorkflow[wf.ID] {
			t.Errorf("duplicate workflow id %q", wf.ID)
		}
		seenWorkflow[wf.ID] = true
		for _, ep := range wf.EntryPoints {
			checkRepoRef(t, root, where+" entry_point", ep)
		}
		for _, st := range wf.Steps {
			for _, ref := range st.CodeRefs {
				checkRepoRef(t, root, where+" step "+st.ID, ref)
			}
		}
	}
}
