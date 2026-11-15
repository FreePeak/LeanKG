// Team notes and annotations: the Rust `leankg note` write, the knowledge
// entry CRUD in db/mod.rs and the promote_environment MCP tool.
package orgknowledge

import (
	"context"
	"errors"
	"strings"

	"github.com/FreePeak/LeanKG/internal/store"
)

// AddNote writes the Rust `leankg note` shape: a KnowledgeEntry of type
// "general", titled "Note for <target>", tagged "note" and anchored on the
// target element or service (target = the element's qualified name).
func (k *Knowledge) AddNote(ctx context.Context, target, content, env, author string) (store.KnowledgeEntry, error) {
	if strings.TrimSpace(target) == "" {
		return store.KnowledgeEntry{}, errors.New("note target is required")
	}
	if strings.TrimSpace(content) == "" {
		return store.KnowledgeEntry{}, errors.New("note content is required")
	}
	if env == "" {
		env = "local"
	}
	if author == "" {
		author = AuthorFromEnv()
	}
	now := k.now()
	entry := store.KnowledgeEntry{
		ID:               NewID("NOTE-"),
		KnowledgeType:    "general",
		Title:            "Note for " + target,
		Content:          content,
		ElementQualified: &target,
		Tags:             "note",
		Environment:      env,
		Author:           author,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := k.st.KnowledgeEntryUpsert(ctx, entry); err != nil {
		return store.KnowledgeEntry{}, err
	}
	return entry, nil
}

// PutAnnotation inserts or replaces any knowledge entry (Rust
// create_knowledge_entry / update_knowledge_entry, both an upsert). It is the
// generic write other surfaces build on: the risky-pattern annotation
// (`leankg pattern`: type "debugging", tags "pattern,risk", content
// "## Context\n<context>\n\n## Solution\n<solution>") and the PRD indexer
// rows are the same call with different field values.
func (k *Knowledge) PutAnnotation(ctx context.Context, entry store.KnowledgeEntry) (store.KnowledgeEntry, error) {
	if entry.ID == "" {
		return store.KnowledgeEntry{}, errors.New("knowledge entry id is required")
	}
	if strings.TrimSpace(entry.Title) == "" {
		return store.KnowledgeEntry{}, errors.New("knowledge entry title is required")
	}
	if entry.KnowledgeType == "" {
		entry.KnowledgeType = "general"
	}
	if entry.Environment == "" {
		entry.Environment = "local"
	}
	if entry.Author == "" {
		entry.Author = AuthorFromEnv()
	}
	now := k.now()
	if entry.CreatedAt == 0 {
		entry.CreatedAt = now
	}
	if entry.UpdatedAt == 0 {
		entry.UpdatedAt = now
	}
	if err := k.st.KnowledgeEntryUpsert(ctx, entry); err != nil {
		return store.KnowledgeEntry{}, err
	}
	return entry, nil
}

// Annotation resolves one knowledge entry (Rust db::get_knowledge_entry).
func (k *Knowledge) Annotation(ctx context.Context, id string) (store.KnowledgeEntry, bool, error) {
	return k.st.KnowledgeEntryByID(ctx, id)
}

// DeleteAnnotation removes one knowledge entry (Rust
// db::delete_knowledge_entry; an absent id is a no-op there too).
func (k *Knowledge) DeleteAnnotation(ctx context.Context, id string) error {
	return k.st.KnowledgeEntryDelete(ctx, id)
}

// NotesFor lists the notes anchored on a target (Rust
// db::get_knowledge_by_element / list_knowledge_by_element).
func (k *Knowledge) NotesFor(ctx context.Context, target string) ([]store.KnowledgeEntry, error) {
	return k.st.KnowledgeEntriesByElement(ctx, target)
}

// AnnotationsForFeature lists the entries anchored on a feature (Rust
// db::get_knowledge_by_feature).
func (k *Knowledge) AnnotationsForFeature(ctx context.Context, featureID string) ([]store.KnowledgeEntry, error) {
	return k.st.KnowledgeEntriesByFeature(ctx, featureID)
}

// AnnotationsForEnvironment lists the newest entries in an environment (Rust
// db::get_knowledge_by_environment).
func (k *Knowledge) AnnotationsForEnvironment(ctx context.Context, environment string, limit int) ([]store.KnowledgeEntry, error) {
	return k.st.KnowledgeEntriesByEnvironment(ctx, environment, limit)
}

// SearchAnnotations is the Rust db::search_knowledge lookup: keyword search
// over title or content, optional exact knowledge type and environment.
// SQLite answers it with the substring search (newest first, no relevance
// signal); the PostgreSQL backend ranks its migration-012 tsvector with
// ts_rank and degrades to substring search internally (issue #273).
func (k *Knowledge) SearchAnnotations(ctx context.Context, query, knowledgeType, environment string, limit int) ([]store.KnowledgeEntry, error) {
	return k.st.KnowledgeEntriesSearch(ctx, query, knowledgeType, environment, limit)
}

// PromoteEnvironment moves every entry staged in the "upcoming" environment
// for a branch into targetEnv (Rust mcp handler::promote_environment). It
// returns how many rows moved; entries without a branch are left alone.
func (k *Knowledge) PromoteEnvironment(ctx context.Context, branch, targetEnv string) (int, error) {
	if strings.TrimSpace(branch) == "" {
		return 0, errors.New("branch is required")
	}
	if strings.TrimSpace(targetEnv) == "" {
		return 0, errors.New("target environment is required")
	}
	// limit 0 = every staged entry (the Rust tool capped the read at 1000).
	entries, err := k.st.KnowledgeEntriesByEnvironment(ctx, "upcoming", 0)
	if err != nil {
		return 0, err
	}
	promoted := 0
	now := k.now()
	for _, entry := range entries {
		if entry.Branch == nil || *entry.Branch != branch {
			continue
		}
		entry.Environment = targetEnv
		entry.UpdatedAt = now
		if err := k.st.KnowledgeEntryUpsert(ctx, entry); err != nil {
			return promoted, err
		}
		promoted++
	}
	return promoted, nil
}
