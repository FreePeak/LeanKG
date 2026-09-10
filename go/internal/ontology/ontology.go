// Package ontology implements the ontology-lite layer of the Go engine: a
// JSON concept catalog loaded from disk, matched case-insensitively against
// indexed elements, with the match set persisted in the store's namespaced
// KV ("ontology").
//
// PARITY GAP (honest scope): this package covers catalog loading and
// matching only. The Rust engine's procedural layer — ontology workflows
// with ordered steps and failure modes, feature-requirement traceability
// (FR -> workflow -> code refs) and the traceability matrix — is NOT
// implemented here and remains an open Go-parity gap.
package ontology

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Concept is one catalog entry.
type Concept struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Aliases     []string `json:"aliases,omitempty"`
	Description string   `json:"description,omitempty"`
}

// Catalog is a loaded concept catalog.
type Catalog struct {
	Concepts []Concept `json:"concepts"`
}

// Match links one concept to one element. Via records the strongest
// provenance: "alias" (an alias matched), "name" (the label equals the
// element name) or "label" (the label is a qualified-name substring).
type Match struct {
	ConceptID string `json:"concept_id"`
	QN        string `json:"qn"`
	Via       string `json:"via"`
}

// viaRank orders provenance preference: alias > name > label.
var viaRank = map[string]int{"alias": 3, "name": 2, "label": 1}

// LoadCatalog reads a catalog JSON file and validates it: concept ids must
// be non-empty and unique.
func LoadCatalog(path string) (*Catalog, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Catalog
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse catalog: %w", err)
	}
	seen := make(map[string]bool, len(c.Concepts))
	for _, con := range c.Concepts {
		if con.ID == "" {
			return nil, fmt.Errorf("catalog concept with empty id")
		}
		if seen[con.ID] {
			return nil, fmt.Errorf("duplicate concept id %q", con.ID)
		}
		seen[con.ID] = true
	}
	return &c, nil
}

// MatchElements matches every concept's aliases and label, case-insensitively,
// against element names (exact) and qualified names (substring). An alias hit
// anywhere wins over a label-name match, which wins over a label-substring
// match. (concept, element) pairs are deduped keeping the strongest
// provenance; the result is sorted by ConceptID then QN.
func (c *Catalog) MatchElements(st store.Backend) ([]Match, error) {
	els, err := st.Elements()
	if err != nil {
		return nil, err
	}
	type key struct{ concept, qn string }
	best := make(map[key]Match)

	for _, con := range c.Concepts {
		label := strings.TrimSpace(con.Label)
		lowerLabel := strings.ToLower(label)
		var aliases []string
		for _, a := range con.Aliases {
			if a = strings.TrimSpace(a); a != "" {
				aliases = append(aliases, strings.ToLower(a))
			}
		}

		for _, el := range els {
			lowerQN := strings.ToLower(el.QualifiedName)
			via := ""
			name := strings.ToLower(strings.TrimSpace(el.Name))
			for _, a := range aliases {
				if a == name || strings.Contains(lowerQN, a) {
					via = "alias"
					break
				}
			}
			if via == "" && label != "" {
				switch {
				case strings.EqualFold(el.Name, label):
					via = "name"
				case strings.Contains(lowerQN, lowerLabel):
					via = "label"
				}
			}
			if via == "" {
				continue
			}
			k := key{con.ID, el.QualifiedName}
			if prev, ok := best[k]; !ok || viaRank[via] > viaRank[prev.Via] {
				best[k] = Match{ConceptID: con.ID, QN: el.QualifiedName, Via: via}
			}
		}
	}

	out := make([]Match, 0, len(best))
	for _, m := range best {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ConceptID != out[j].ConceptID {
			return out[i].ConceptID < out[j].ConceptID
		}
		return out[i].QN < out[j].QN
	})
	return out, nil
}

// SaveMatches persists the match set under the "ontology" KV namespace.
func SaveMatches(st store.Backend, matches []Match) error {
	b, err := json.Marshal(matches)
	if err != nil {
		return err
	}
	return st.KVSet("ontology", "matches", string(b))
}

// LoadMatches reads the match set back; a missing key yields (nil, nil).
func LoadMatches(st store.Backend) ([]Match, error) {
	s, ok, err := st.KVGet("ontology", "matches")
	if err != nil || !ok {
		return nil, err
	}
	var matches []Match
	if err := json.Unmarshal([]byte(s), &matches); err != nil {
		return nil, fmt.Errorf("parse stored matches: %w", err)
	}
	return matches, nil
}
