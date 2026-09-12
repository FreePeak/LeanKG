// Package prdindex indexes product-requirement documents: it extracts FR-*
// feature requirements (with their acceptance criteria and the code paths they
// name) and US-* user stories from PRD markdown, lands them as graph entities
// and links every requirement to the ontology workflows that implement it.
//
// Rust reference (deleted tree, rev f7624143^): src/prd_indexer/mod.rs (the
// parser and the knowledge_entries row projection) plus the index_prd MCP tool
// in src/mcp/handler.rs, which upserted those rows and then matched each
// requirement's code paths against workflow_step metadata to write
// feature_workflow_links (feature_id -> workflow_id) rows.
//
// Storage mapping: the Rust engine kept requirements in its single Cozo
// knowledge_entries relation and the feature/workflow pairs in a second
// relation. The Go engine stores the entities as code elements (element types
// prd_requirement / prd_user_story) and each feature/workflow pair as an
// `implemented_by` edge from the requirement to the workflow GID. Element
// storage is what keeps the link edges resolvable — doctor --deep flags
// relationships whose endpoints are not elements — and what makes every
// requirement reachable through the 3-tool query envelope with no extra
// wiring (an exact query for the FR/US id lands on the element name).
// Requirements are indexed under the synthetic file path "prd://<doc>"
// (the same convention as ontology:// and session://), so a re-index is a
// clean delete-by-file rebuild that can never touch the document elements
// internal/docindex writes for the same markdown file. KnowledgeEntries
// projects the same parse onto the Rust knowledge_entries row shape for the
// org-knowledge surface.
package prdindex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Element types of the requirement layer.
const (
	TypeRequirement = "prd_requirement"
	TypeUserStory   = "prd_user_story"
)

// RelImplementedBy links a requirement entity to an ontology workflow that
// implements it (the Go shape of the Rust feature_workflow_links relation).
// Source is the requirement element qualified name, target the workflow GID.
const RelImplementedBy = "implemented_by"

// knowledgeType is the Rust knowledge_type for PRD rows.
const knowledgeType = "prd_mapping"

// author is the Rust knowledge-entry author for PRD rows.
const author = "prd_indexer"

// filePrefix is the synthetic file path prefix of PRD entities.
const filePrefix = "prd://"

// language of the source document the entities were parsed from.
const language = "markdown"

// maxContent bounds one entity's stored content, in characters (docindex uses
// the same 8000-character ceiling for a markdown section).
const maxContent = 8000

// Requirement is a parsed FR-* feature requirement.
type Requirement struct {
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	Priority           string   `json:"priority"` // "Must Have" | "Should Have" | "Could Have"
	Focus              string   `json:"focus"`    // "P0" | "P1" | "P2" | "P3"
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	UserStoryIDs       []string `json:"user_story_ids,omitempty"`
	RelatedFRIDs       []string `json:"related_fr_ids,omitempty"`
	CodePaths          []string `json:"code_paths,omitempty"`
	Line               int      `json:"line,omitempty"`     // 1-based definition line, 0 when only mentioned in prose
	LineEnd            int      `json:"line_end,omitempty"` // last line of the definition block
}

// UserStory is a parsed US-* user story.
type UserStory struct {
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	Priority           string   `json:"priority"`
	Focus              string   `json:"focus"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	FeatureIDs         []string `json:"feature_ids,omitempty"`
	CodePaths          []string `json:"code_paths,omitempty"`
	Line               int      `json:"line,omitempty"`
	LineEnd            int      `json:"line_end,omitempty"`
}

// Result is one parsed PRD document.
type Result struct {
	Requirements []Requirement `json:"requirements"`
	UserStories  []UserStory   `json:"user_stories"`
	Errors       []string      `json:"errors"`
}

// IndexResult summarizes one Index run.
type IndexResult struct {
	Source           string
	Requirements     int
	UserStories      int
	Elements         int
	KnowledgeEntries int
	Created          int
	Updated          int
	Removed          int
	WorkflowLinks    int
	Errors           []string
}

// Metadata keys of a PRD element, mirroring the Rust knowledge_entries
// columns so a reader that knows the Rust row shape can read these entities.
const (
	MetaKnowledgeType = "knowledge_type"
	MetaTitle         = "title"
	MetaFeatureID     = "feature_id"
	MetaUserStoryIDs  = "user_story_ids"
	MetaUserStoryID   = "user_story_id"
	MetaFeatureIDs    = "feature_ids"
	MetaRelatedFRIDs  = "related_fr_ids"
	MetaPriority      = "priority"
	MetaFocus         = "focus"
	MetaTags          = "tags"
	MetaAuthor        = "author"
	MetaDoc           = "doc"
	MetaEnvironment   = "environment"
	MetaCodePaths     = "code_paths"
	MetaAC            = "acceptance_criteria"
	MetaCreatedAt     = "created_at"
	MetaUpdatedAt     = "updated_at"
)

// Id shapes (Rust parity: standalone mentions need at least two
// hyphen-separated segments, so prose fragments like "FR-05" are not picked
// up), the definition-block table row, backtick code paths and AC lines.
var (
	frRe = regexp.MustCompile(`\b(FR-[A-Z][A-Z0-9]*(?:-[A-Z0-9]+)+)\b`)
	usRe = regexp.MustCompile(`\b(US-[A-Z][A-Z0-9]*(?:-[A-Z0-9]+)+)\b`)

	// statusRe and dateRe match cells of the tracking tables that surround a
	// requirement's description (status/date columns) and are not part of it.
	statusRe = regexp.MustCompile(`(?i)^(done|not[ _-]?done|in[ _-]?progress|todo|pending|partial|open|closed|folded|deferred|wip|obsolete|superseded)$`)
	dateRe   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

	codePathRe = regexp.MustCompile("`([a-zA-Z0-9_/.-]+\\.[a-zA-Z]+(?:::[a-zA-Z0-9_]+)?(?:\\([^)]*\\))?)`")

	defRe = regexp.MustCompile(`^\s*(?:\*\*|#{1,6}\s+)?([A-Z]+-[A-Z0-9.-]+(?:\s*/\s*[A-Z]+-[A-Z0-9.-]+)*)\s*(?:—|–|--|:)\s*(.+?)\s*$`)

	acRe = regexp.MustCompile(`(?i)^\s*(?:[-*+]\s+)?(?:\*\*)?(?:AC|acceptance criteria)(?:\*\*)?\s*[:：]\s*(.*)$`)

	priorityRe = regexp.MustCompile(`(?i)^(must have|should have|could have|won't have)$`)
	focusRe    = regexp.MustCompile(`^P[0-3]$`)
	idPartRe   = regexp.MustCompile(`^[A-Z]+-[A-Z0-9.-]+$`)
)

// Parse extracts FR-* and US-* entities from PRD markdown. Ported from
// parse_prd_markdown plus the definition-block form this repository's PRD
// actually uses ("**FR-X — Title (Priority, Pn)**" followed by bullets, with
// acceptance criteria on "- AC: ..." lines): the Rust parser only read table
// rows and bare id mentions, so it produced description-less entities for a
// document written in the block form.
func Parse(content string) Result {
	var res Result
	reqIdx := map[string]int{}
	usIdx := map[string]int{}
	defTitle := map[string]string{}

	var (
		blockIDs   []string // ids of the open definition block
		blockStart int      // 1-based first line, 0 = no block open
		fenced     bool
	)

	requirement := func(id string) *Requirement {
		if i, ok := reqIdx[id]; ok {
			return &res.Requirements[i]
		}
		res.Requirements = append(res.Requirements, Requirement{ID: id, Title: id})
		reqIdx[id] = len(res.Requirements) - 1
		return &res.Requirements[len(res.Requirements)-1]
	}
	userStory := func(id string) *UserStory {
		if i, ok := usIdx[id]; ok {
			return &res.UserStories[i]
		}
		res.UserStories = append(res.UserStories, UserStory{ID: id, Title: id})
		usIdx[id] = len(res.UserStories) - 1
		return &res.UserStories[len(res.UserStories)-1]
	}
	forEachBlock := func(fn func(id string)) {
		for _, id := range blockIDs {
			fn(id)
		}
	}
	// flush closes the open definition block: entities first defined on the
	// block's opening line end there.
	flush := func(end int) {
		forEachBlock(func(id string) {
			if strings.HasPrefix(id, "FR-") {
				r := requirement(id)
				if r.Line == blockStart {
					r.LineEnd = end
				}
			} else {
				s := userStory(id)
				if s.Line == blockStart {
					s.LineEnd = end
				}
			}
		})
	}

	lines := strings.Split(content, "\n")
	unfenced := unfencedText(content)
	for i, raw := range lines {
		lineNo := i + 1
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}

		// A section heading that is not itself a definition closes the open
		// definition block: body lines past it belong to the new section.
		if !strings.HasPrefix(trimmed, "**") && strings.HasPrefix(trimmed, "#") && !defRe.MatchString(trimmed) {
			flush(lineNo - 1)
			blockIDs, blockStart = nil, 0
		}
		if frs, uss, priority, focus, desc, ok := parseTableRow(raw); ok {
			flush(lineNo - 1)
			blockIDs, blockStart = nil, 0
			paths := codePaths(desc)
			for _, id := range frs {
				r := requirement(id)
				r.Line, r.LineEnd = lineNo, lineNo
				applyMeta(r, nil, priority, focus)
				if r.Description == "" {
					r.Description = desc
				}
				r.CodePaths = mergeStrings(r.CodePaths, paths)
				r.UserStoryIDs = mergeStrings(r.UserStoryIDs, uss)
				r.RelatedFRIDs = mergeStrings(r.RelatedFRIDs, without(frs, id))
			}
			for _, id := range uss {
				s := userStory(id)
				s.Line, s.LineEnd = lineNo, lineNo
				applyMeta(nil, s, priority, focus)
				if s.Description == "" {
					s.Description = desc
				}
				s.CodePaths = mergeStrings(s.CodePaths, paths)
				s.FeatureIDs = mergeStrings(s.FeatureIDs, frs)
			}
			continue
		}

		if m := defRe.FindStringSubmatch(trimmed); m != nil {
			ids := splitIDsAll(strings.TrimSpace(strings.Trim(m[1], "*")))
			if len(ids) > 0 {
				flush(lineNo - 1)
				title, priority, focus := splitTitleMeta(strings.TrimRight(strings.TrimSpace(m[2]), "*"))
				blockIDs, blockStart = ids, lineNo
				for _, id := range ids {
					if title != "" {
						if prev, ok := defTitle[id]; ok && prev != title {
							res.Errors = append(res.Errors,
								fmt.Sprintf("conflicting definitions for %s", id))
						}
						defTitle[id] = title
					}
					if strings.HasPrefix(id, "FR-") {
						r := requirement(id)
						r.Line = lineNo
						if title != "" && (r.Title == "" || r.Title == id) {
							r.Title = title
						}
						applyMeta(r, nil, priority, focus)
					} else {
						s := userStory(id)
						s.Line = lineNo
						if title != "" && (s.Title == "" || s.Title == id) {
							s.Title = title
						}
						applyMeta(nil, s, priority, focus)
					}
				}
				continue
			}
		}

		if m := acRe.FindStringSubmatch(trimmed); m != nil {
			if ac := strings.TrimSpace(strings.Trim(m[1], "*")); ac != "" {
				forEachBlock(func(id string) {
					if strings.HasPrefix(id, "FR-") {
						r := requirement(id)
						r.AcceptanceCriteria = append(r.AcceptanceCriteria, ac)
					} else {
						s := userStory(id)
						s.AcceptanceCriteria = append(s.AcceptanceCriteria, ac)
					}
				})
			}
			continue
		}

		// Definition-block body: accumulate the description and collect the
		// code paths it names.
		if blockStart != 0 {
			if body := bodyLine(trimmed); body != "" {
				paths := codePaths(body)
				forEachBlock(func(id string) {
					if strings.HasPrefix(id, "FR-") {
						r := requirement(id)
						r.Description = joinParagraph(r.Description, body)
						r.CodePaths = mergeStrings(r.CodePaths, paths)
					} else {
						s := userStory(id)
						s.Description = joinParagraph(s.Description, body)
						s.CodePaths = mergeStrings(s.CodePaths, paths)
					}
				})
			}
		}
	}
	flush(len(lines))

	// Standalone mentions outside tables and definitions fill in ids no other
	// form produced (Rust parity: prose like "US-X / FR-Y" still yields rows).
	for _, id := range frRe.FindAllString(unfenced, -1) {
		requirement(id)
	}
	for _, id := range usRe.FindAllString(unfenced, -1) {
		userStory(id)
	}
	return res
}

// Index parses content and replaces the entities of docPath in the store:
// prd_requirement / prd_user_story elements plus one implemented_by edge per
// (requirement, workflow) pair the document names. Re-indexing is a clean
// rebuild keyed on the synthetic file path, so it is idempotent for edits that
// remove requirements too, not just for re-reads of an unchanged document.
func Index(ctx context.Context, st store.Backend, docPath, content, env string) (IndexResult, error) {
	res := Parse(content)
	out := IndexResult{
		Source:       docPath,
		Requirements: len(res.Requirements),
		UserStories:  len(res.UserStories),
		Errors:       res.Errors,
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}

	els := Elements(res, docPath, env, time.Now().Unix())
	out.Elements = len(els)

	// One element scan serves three purposes: the created/updated counts (the
	// Rust index_prd counted an already-present row as an update), the ids
	// this document wrote last time (stale rows to drop) and the workflow
	// steps the link pass matches against.
	all, err := st.Elements()
	if err != nil {
		return out, fmt.Errorf("prdindex: read elements: %w", err)
	}
	prev := map[string]bool{}
	for _, e := range all {
		if e.FilePath == filePrefix+docPath {
			prev[e.QualifiedName] = true
		}
	}
	current := make(map[string]bool, len(els))
	for _, el := range els {
		current[el.QualifiedName] = true
		if prev[el.QualifiedName] {
			out.Updated++
		} else {
			out.Created++
		}
	}
	// Rows of requirements the document no longer defines: element ids and
	// knowledge-entry ids are the same key, so both projections drop together
	// and the two query surfaces cannot drift.
	for qn := range prev {
		if current[qn] {
			continue
		}
		out.Removed++
		if err := st.KnowledgeEntryDelete(qn); err != nil {
			return out, fmt.Errorf("prdindex: drop stale row %s: %w", qn, err)
		}
	}

	// Rebuild: "prd://<doc>" is unique to this document, so the delete can
	// only touch these entities — never the doc elements internal/docindex
	// writes for the same markdown file.
	if err := st.DeleteByFile(filePrefix + docPath); err != nil {
		return out, fmt.Errorf("prdindex: clear %s: %w", docPath, err)
	}

	// The knowledge rows are the Rust engine's original requirement store
	// (search_knowledge / get_knowledge_by_feature read them); the graph
	// entities above are what makes them traceable to workflows.
	now := time.Now().Unix()
	entries := KnowledgeEntries(res, env)
	for i := range entries {
		entries[i].CreatedAt, entries[i].UpdatedAt = now, now
		if err := st.KnowledgeEntryUpsert(entries[i]); err != nil {
			return out, fmt.Errorf("prdindex: knowledge upsert %s: %w", entries[i].ID, err)
		}
	}
	out.KnowledgeEntries = len(entries)

	if len(els) == 0 {
		return out, nil
	}
	if err := st.UpsertElements(els); err != nil {
		return out, fmt.Errorf("prdindex: upsert elements: %w", err)
	}
	rels := linksFrom(workflowSteps(all), res, docPath)
	if err := st.UpsertRelationships(rels); err != nil {
		return out, fmt.Errorf("prdindex: upsert links: %w", err)
	}
	out.WorkflowLinks = len(rels)
	return out, nil
}

// IndexDocument reads projectRoot/relDoc and indexes it. A missing document is
// an error (the Rust index_prd refused to run without its source).
func IndexDocument(ctx context.Context, st store.Backend, projectRoot, relDoc, env string) (IndexResult, error) {
	path := filepath.Join(projectRoot, filepath.FromSlash(relDoc))
	content, err := os.ReadFile(path)
	if err != nil {
		return IndexResult{Source: relDoc}, fmt.Errorf("prdindex: read %s: %w", relDoc, err)
	}
	return Index(ctx, st, filepath.ToSlash(relDoc), string(content), env)
}

// Elements renders the parsed entities as store elements. now is stamped into
// the created_at/updated_at metadata of every entity (the Rust rows carried
// both fields).
func Elements(res Result, docPath, env string, now int64) []store.Element {
	els := make([]store.Element, 0, len(res.Requirements)+len(res.UserStories))
	for _, req := range res.Requirements {
		els = append(els, store.Element{
			QualifiedName: requirementQN(req.ID),
			ElementType:   TypeRequirement,
			Name:          req.ID,
			FilePath:      filePrefix + docPath,
			LineStart:     req.Line,
			LineEnd:       req.LineEnd,
			Language:      language,
			Content:       contentFor(req),
			Metadata: map[string]any{
				MetaKnowledgeType: knowledgeType,
				MetaTitle:         req.ID + " " + req.Title,
				MetaFeatureID:     req.ID,
				MetaPriority:      req.Priority,
				MetaFocus:         req.Focus,
				MetaTags:          tags(req.Priority, req.Focus),
				MetaAuthor:        author,
				MetaDoc:           docPath,
				MetaEnvironment:   env,
				MetaCodePaths:     req.CodePaths,
				MetaUserStoryIDs:  req.UserStoryIDs,
				MetaRelatedFRIDs:  req.RelatedFRIDs,
				MetaAC:            req.AcceptanceCriteria,
				MetaCreatedAt:     now,
				MetaUpdatedAt:     now,
			},
		})
	}
	for _, us := range res.UserStories {
		els = append(els, store.Element{
			QualifiedName: userStoryQN(us.ID),
			ElementType:   TypeUserStory,
			Name:          us.ID,
			FilePath:      filePrefix + docPath,
			LineStart:     us.Line,
			LineEnd:       us.LineEnd,
			Language:      language,
			Content:       contentForUS(us),
			Metadata: map[string]any{
				MetaKnowledgeType: knowledgeType,
				MetaTitle:         us.ID + " " + us.Title,
				MetaUserStoryID:   us.ID,
				MetaPriority:      us.Priority,
				MetaFocus:         us.Focus,
				MetaTags:          tags(us.Priority, us.Focus),
				MetaAuthor:        author,
				MetaDoc:           docPath,
				MetaEnvironment:   env,
				MetaCodePaths:     us.CodePaths,
				MetaFeatureIDs:    us.FeatureIDs,
				MetaAC:            us.AcceptanceCriteria,
				MetaCreatedAt:     now,
				MetaUpdatedAt:     now,
			},
		})
	}
	return els
}

// Links returns the implemented_by edges from every parsed requirement to each
// ontology workflow element that implements it. Two signals are used, both
// mirroring the Rust index_prd:
//
//   - code path: a workflow_step whose metadata mentions one of the
//     requirement's code paths (Rust matched the raw metadata JSON with
//     str_contains);
//   - feature_ids / user_story_ids: a workflow_step whose metadata lists the
//     requirement id or one of its user story ids, which is the linkage the Go
//     ontology layer already traces (ontology.TraceFeature).
//
// An edge is emitted only for steps whose parent is a stored workflow, so
// every endpoint resolves to an element.
func Links(st store.Backend, res Result, docPath string) ([]store.Relationship, error) {
	if len(res.Requirements) == 0 {
		return nil, nil
	}
	all, err := st.Elements()
	if err != nil {
		return nil, fmt.Errorf("prdindex: read elements: %w", err)
	}
	return linksFrom(workflowSteps(all), res, docPath), nil
}

// workflowSteps filters an element set down to the ontology workflow steps the
// link pass matches against.
func workflowSteps(all []store.Element) []store.Element {
	steps := make([]store.Element, 0, len(all))
	for _, el := range all {
		if el.ElementType == "workflow_step" {
			steps = append(steps, el)
		}
	}
	return steps
}

// linksFrom is the matching half of Links, over an element set the caller has
// already read (Index reads it once for its own bookkeeping).
func linksFrom(steps []store.Element, res Result, docPath string) []store.Relationship {
	if len(steps) == 0 {
		return nil
	}
	var rels []store.Relationship
	for _, req := range res.Requirements {
		matched := map[string]string{} // workflow GID -> the value that matched
		for _, p := range req.CodePaths {
			filePart := p
			if i := strings.Index(p, "::"); i >= 0 {
				filePart = p[:i]
			}
			if filePart == "" {
				continue
			}
			for _, step := range steps {
				wf := workflowOf(step)
				if wf == "" || matched[wf] != "" {
					continue
				}
				if metaMentions(step, filePart) {
					matched[wf] = filePart
				}
			}
		}
		for _, step := range steps {
			wf := workflowOf(step)
			if wf == "" || matched[wf] != "" {
				continue
			}
			if hasString(step.Metadata["feature_ids"], req.ID) {
				matched[wf] = req.ID
				continue
			}
			for _, us := range req.UserStoryIDs {
				if hasString(step.Metadata["user_story_ids"], us) {
					matched[wf] = us
					break
				}
			}
		}
		for _, wf := range sortedKeys(matched) {
			rels = append(rels, store.Relationship{
				Source:     requirementQN(req.ID),
				Target:     wf,
				RelType:    RelImplementedBy,
				Confidence: 1.0,
				Metadata: map[string]any{
					"match":            matched[wf],
					"doc":              docPath,
					"confidence_label": "EXTRACTED",
				},
			})
		}
	}
	return rels
}

// RequirementByID returns the requirement entity for a feature id.
func RequirementByID(st store.Backend, id string) (store.Element, bool, error) {
	return elementByQN(st, requirementQN(id))
}

// UserStoryByID returns the user story entity for a user story id.
func UserStoryByID(st store.Backend, id string) (store.Element, bool, error) {
	return elementByQN(st, userStoryQN(id))
}

// Requirements lists every indexed requirement entity, ordered by qualified
// name.
func Requirements(st store.Backend) ([]store.Element, error) {
	return elementsByType(st, TypeRequirement)
}

// UserStories lists every indexed user story entity, ordered by qualified
// name.
func UserStories(st store.Backend) ([]store.Element, error) {
	return elementsByType(st, TypeUserStory)
}

// WorkflowsForFeature returns the workflow GIDs linked to a feature id (the
// get_workflows_for_feature read over the implemented_by edges).
func WorkflowsForFeature(st store.Backend, featureID string) ([]string, error) {
	rels, err := st.Outgoing(requirementQN(featureID))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range rels {
		if r.RelType == RelImplementedBy {
			out = append(out, r.Target)
		}
	}
	return out, nil
}

// FeaturesForWorkflow returns the feature ids a workflow implements (the
// get_features_for_workflow read).
func FeaturesForWorkflow(st store.Backend, workflowGID string) ([]string, error) {
	rels, err := st.Incoming(workflowGID)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range rels {
		if r.RelType != RelImplementedBy {
			continue
		}
		if id, ok := strings.CutPrefix(r.Source, "prd-req-"); ok {
			out = append(out, id)
		}
	}
	return out, nil
}

// KnowledgeEntries projects the parsed entities onto the store's knowledge
// rows — the Rust requirements_to_knowledge_entries /
// user_stories_to_knowledge_entries pair: ids "prd-req-<FR>"/"prd-us-<US>"
// (the same key as the graph entity), knowledge_type "prd_mapping",
// feature_id / user_story_id cross-links, tags "<priority>,<focus>" and author
// "prd_indexer". CreatedAt/UpdatedAt are stamped by the caller.
func KnowledgeEntries(res Result, env string) []store.KnowledgeEntry {
	out := make([]store.KnowledgeEntry, 0, len(res.Requirements)+len(res.UserStories))
	for _, req := range res.Requirements {
		e := store.KnowledgeEntry{
			ID:            "prd-req-" + req.ID,
			KnowledgeType: knowledgeType,
			Title:         req.ID + " " + req.Title,
			Content:       contentFor(req),
			FeatureID:     strPtr(req.ID),
			Tags:          tags(req.Priority, req.Focus),
			Environment:   env,
			Author:        author,
		}
		if len(req.UserStoryIDs) > 0 {
			e.UserStoryID = strPtr(strings.Join(req.UserStoryIDs, ","))
		}
		out = append(out, e)
	}
	for _, us := range res.UserStories {
		e := store.KnowledgeEntry{
			ID:            "prd-us-" + us.ID,
			KnowledgeType: knowledgeType,
			Title:         us.ID + " " + us.Title,
			Content:       contentForUS(us),
			UserStoryID:   strPtr(us.ID),
			Tags:          tags(us.Priority, us.Focus),
			Environment:   env,
			Author:        author,
		}
		if len(us.FeatureIDs) > 0 {
			e.FeatureID = strPtr(strings.Join(us.FeatureIDs, ","))
		}
		out = append(out, e)
	}
	return out
}

// elementByQN loads one element by its qualified name.
func elementByQN(st store.Backend, qn string) (store.Element, bool, error) {
	found, err := st.FindExact(qn)
	if err != nil {
		return store.Element{}, false, err
	}
	for _, e := range found {
		if e.QualifiedName == qn {
			return e, true, nil
		}
	}
	return store.Element{}, false, nil
}

func elementsByType(st store.Backend, typ string) ([]store.Element, error) {
	all, err := st.Elements()
	if err != nil {
		return nil, err
	}
	out := make([]store.Element, 0, 16)
	for _, e := range all {
		if e.ElementType == typ {
			out = append(out, e)
		}
	}
	return out, nil
}

func requirementQN(id string) string { return "prd-req-" + id }
func userStoryQN(id string) string   { return "prd-us-" + id }

// contentFor renders a requirement's content. The header block is the Rust
// knowledge row content verbatim; the acceptance-criteria section is additive
// (the Rust parser dropped AC lines entirely).
func contentFor(req Requirement) string {
	body := fmt.Sprintf(
		"Priority: %s | Focus: %s\n\nDescription: %s\n\nRelated FRs: %s\n\nRelated US: %s\n\nCode paths: %s",
		req.Priority, req.Focus, req.Description,
		strings.Join(req.RelatedFRIDs, ", "), strings.Join(req.UserStoryIDs, ", "),
		strings.Join(req.CodePaths, ", "))
	return withAC(body, req.AcceptanceCriteria)
}

// contentForUS renders a user story's content (the Rust user-story content
// header: priority/focus/description/related FRs).
func contentForUS(us UserStory) string {
	body := fmt.Sprintf(
		"Priority: %s | Focus: %s\n\nDescription: %s\n\nRelated FRs: %s",
		us.Priority, us.Focus, us.Description, strings.Join(us.FeatureIDs, ", "))
	return withAC(body, us.AcceptanceCriteria)
}

func withAC(body string, acs []string) string {
	if len(acs) == 0 {
		return truncate(body)
	}
	var b strings.Builder
	b.WriteString(body)
	b.WriteString("\n\nAcceptance criteria:")
	for _, ac := range acs {
		b.WriteString("\n- ")
		b.WriteString(ac)
	}
	return truncate(b.String())
}

func tags(priority, focus string) string {
	return strings.ReplaceAll(priority, " ", "-") + "," + focus
}

// splitIDsAll splits a compound id list ("US-A / FR-B / FR-C-01..03"),
// keeping only well-formed ids: it classifies a table-row id cell and a
// definition line that names several ids alike.
func splitIDsAll(field string) []string {
	var out []string
	for _, part := range strings.Split(field, "/") {
		part = strings.TrimSpace(part)
		if idPartRe.MatchString(part) && (strings.HasPrefix(part, "FR-") || strings.HasPrefix(part, "US-")) {
			out = append(out, part)
		}
	}
	return out
}

// splitTitleMeta splits "Title (Must Have, P0; extra)" into its title and the
// priority/focus it declares. Parentheses without a priority or focus are part
// of the title.
func splitTitleMeta(text string) (title, priority, focus string) {
	text = strings.TrimSpace(strings.Trim(text, "*"))
	for i := range len(text) {
		if text[i] != '(' {
			continue
		}
		end := strings.IndexByte(text[i:], ')')
		if end < 0 {
			break
		}
		p, f := metaFrom(text[i+1 : i+end])
		if p == "" && f == "" {
			continue
		}
		return strings.TrimSpace(strings.Trim(text[:i], "-—–")), p, f
	}
	return text, "", ""
}

// metaFrom reads the priority ("Must Have"/"Should Have"/"Could Have") and the
// focus ("P0".."P3") out of a parenthetical group. Each comma-separated segment
// is checked at its head, so trailing prose after ';' is ignored.
func metaFrom(inner string) (priority, focus string) {
	for _, seg := range strings.Split(inner, ",") {
		head := strings.TrimSpace(strings.SplitN(seg, ";", 2)[0])
		switch {
		case priority == "" && priorityRe.MatchString(head):
			priority = canonicalPriority(head)
		case focus == "" && focusRe.MatchString(head):
			focus = head
		}
	}
	return priority, focus
}

func canonicalPriority(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "must have":
		return "Must Have"
	case "should have":
		return "Should Have"
	case "could have":
		return "Could Have"
	case "won't have":
		return "Won't Have"
	}
	return s
}

// applyMeta fills priority/focus on a requirement or a user story (exactly one
// pointer is non-nil).
func applyMeta(r *Requirement, us *UserStory, priority, focus string) {
	if r != nil {
		if r.Priority == "" {
			r.Priority = priority
		}
		if r.Focus == "" {
			r.Focus = focus
		}
		return
	}
	if us.Priority == "" {
		us.Priority = priority
	}
	if us.Focus == "" {
		us.Focus = focus
	}
}

// bodyLine normalizes one definition-body line: list markers and bold markers
// are stripped, headings and table rows are dropped.
func bodyLine(trimmed string) string {
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "|") {
		return ""
	}
	trimmed = strings.TrimPrefix(trimmed, "- ")
	trimmed = strings.TrimPrefix(trimmed, "* ")
	trimmed = strings.TrimPrefix(trimmed, "+ ")
	return strings.TrimSpace(strings.Trim(trimmed, "*"))
}

// joinParagraph appends one body line to an accumulating description.
func joinParagraph(desc, line string) string {
	if desc == "" {
		return line
	}
	return desc + "\n" + line
}

// codePaths extracts the backtick-quoted code paths of a text fragment.
func codePaths(text string) []string {
	var out []string
	for _, m := range codePathRe.FindAllStringSubmatch(text, -1) {
		out = append(out, m[1])
	}
	return out
}

// metaMentions reports whether a step's metadata JSON mentions value, matching
// the Rust str_contains(metadata, value) test, and additionally whether the
// step's name does (a workflow step named after its source file is common).
func metaMentions(step store.Element, value string) bool {
	if b, err := json.Marshal(step.Metadata); err == nil && strings.Contains(string(b), value) {
		return true
	}
	return strings.Contains(step.Name, value)
}

// workflowOf returns the workflow GID a step belongs to (parent_qualified, or
// the workflow_gid metadata field).
func workflowOf(step store.Element) string {
	if step.ParentQualified != "" {
		return step.ParentQualified
	}
	if s, ok := step.Metadata["workflow_gid"].(string); ok {
		return s
	}
	return ""
}

// hasString reports whether v is a string or a string list containing want.
func hasString(v any, want string) bool {
	switch t := v.(type) {
	case string:
		return t == want
	case []string:
		for _, s := range t {
			if s == want {
				return true
			}
		}
	case []any:
		for _, s := range t {
			if s == want {
				return true
			}
		}
	}
	return false
}

func without(ids []string, drop string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != drop {
			out = append(out, id)
		}
	}
	return out
}

func mergeStrings(dst, add []string) []string {
	for _, s := range add {
		if s == "" {
			continue
		}
		dup := false
		for _, d := range dst {
			if d == s {
				dup = true
				break
			}
		}
		if !dup {
			dst = append(dst, s)
		}
	}
	return dst
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func strPtr(s string) *string { return &s }

func truncate(s string) string {
	if len(s) <= maxContent {
		return s
	}
	cut := s[:maxContent]
	for len(cut) > 0 && !utf8.RuneStart(cut[len(cut)-1]) {
		cut = cut[:len(cut)-1]
	}
	return cut
}

// unfencedText drops fenced code blocks from a markdown document so scanners
// that run over the whole text never match code samples.
func unfencedText(content string) string {
	var b strings.Builder
	fenced := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// parseTableRow reads one markdown table row that carries requirement ids in
// one of its cells. It replaces the Rust simple_row_re, which only matched the
// "| ID | Priority | Focus | Intent |" column order: this repository's PRD
// tables put the id in a later cell and the status in the last one
// ("| 1 | `FR-3T-01` | **P0** | Registry: ... | **IN_PROGRESS** |"), and the
// tracker tables carry no priority cell at all.
//
// ponytail: cell classification is a heuristic — a cell that is exactly a
// status token, an ISO date or a bare number is treated as a tracking column
// rather than description text; the upgrade path is to read the tracker's
// status into element metadata instead of dropping it.
func parseTableRow(line string) (frs, uss []string, priority, focus, desc string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 2 || !strings.HasPrefix(trimmed, "|") || !strings.HasSuffix(trimmed, "|") {
		return nil, nil, "", "", "", false
	}
	raw := strings.Split(strings.Trim(trimmed, "|"), "|")
	cells := make([]string, len(raw))
	// Bold markers are emphasis, not text: a table cell like
	// "**DONE** on feat/x" must not leave a dangling "**" in the description.
	for i, c := range raw {
		cells[i] = strings.Trim(strings.ReplaceAll(strings.TrimSpace(c), "**", ""), "`")
	}

	idCell := -1
	for i, c := range cells {
		ids := splitIDsAll(c)
		if len(ids) == 0 {
			continue
		}
		for _, id := range ids {
			if strings.HasPrefix(id, "FR-") {
				frs = append(frs, id)
			} else {
				uss = append(uss, id)
			}
		}
		idCell = i
		break
	}
	if idCell < 0 {
		return nil, nil, "", "", "", false
	}

	var body []string
	for i, c := range cells {
		if i == idCell || c == "" {
			continue
		}
		switch {
		case focus == "" && focusRe.MatchString(c):
			focus = c
		case priority == "" && priorityRe.MatchString(c):
			priority = canonicalPriority(c)
		case statusRe.MatchString(c) || dateRe.MatchString(c) || isDigits(c):
			// A tracking column (status, date, row number), not description.
		default:
			body = append(body, c)
		}
	}
	return frs, uss, priority, focus, strings.Join(body, " | "), true
}

// isDigits reports whether s is a bare non-empty decimal number.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// Coverage is one requirement's traceability row: the requirement entity's
// parsed fields plus the workflows linked to it (the Rust
// get_traceability_matrix row joined with get_workflows_for_feature).
type Coverage struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Priority    string   `json:"priority,omitempty"`
	Focus       string   `json:"focus,omitempty"`
	Description string   `json:"description,omitempty"`
	AC          []string `json:"acceptance_criteria,omitempty"`
	CodePaths   []string `json:"code_paths,omitempty"`
	UserStories []string `json:"user_story_ids,omitempty"`
	Workflows   []string `json:"workflows"`
}

// Trace returns one Coverage row per indexed requirement, or the single row of
// featureID when it is non-empty. Rows are ordered by feature id.
func Trace(st store.Backend, featureID string) ([]Coverage, error) {
	var reqs []store.Element
	if featureID != "" {
		el, ok, err := RequirementByID(st, featureID)
		if err != nil {
			return nil, err
		}
		if ok {
			reqs = append(reqs, el)
		}
	} else {
		var err error
		reqs, err = Requirements(st)
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(reqs, func(i, j int) bool { return reqs[i].Name < reqs[j].Name })

	out := make([]Coverage, 0, len(reqs))
	for _, el := range reqs {
		wfs, err := WorkflowsForFeature(st, el.Name)
		if err != nil {
			return nil, err
		}
		out = append(out, Coverage{
			ID:          el.Name,
			Title:       metaString(el, MetaTitle),
			Priority:    metaString(el, MetaPriority),
			Focus:       metaString(el, MetaFocus),
			Description: el.Content,
			AC:          metaStrings(el, MetaAC),
			CodePaths:   metaStrings(el, MetaCodePaths),
			UserStories: metaStrings(el, MetaUserStoryIDs),
			Workflows:   wfs,
		})
	}
	return out, nil
}

// metaString reads one string metadata field.
func metaString(el store.Element, key string) string {
	if s, ok := el.Metadata[key].(string); ok {
		return s
	}
	return ""
}

// metaStrings reads one string-list metadata field (JSON decoding yields
// []any).
func metaStrings(el store.Element, key string) []string {
	switch v := el.Metadata[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, s := range v {
			if str, ok := s.(string); ok {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}
