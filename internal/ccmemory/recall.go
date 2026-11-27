package ccmemory

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/FreePeak/LeanKG/internal/memory"
)

// Recall scopes (KD-9).
const (
	scopeProject = "project"
	scopeGlobal  = "global"
	scopeAll     = "all"
)

// recallCommand is the command name in every RecallResult.
const recallCommand = "agent_recall"

var (
	validScopes = []string{scopeProject, scopeGlobal, scopeAll}
	validTypes  = []string{string(KindUser), string(KindFeedback), string(KindProject), string(KindReference), string(KindUnknown)}
)

// RecallRequest is one agent_recall call. Empty Scope means project and a
// zero Limit means memory.RecallLimit. Caller is the caller's resolved project
// slug and may be empty.
type RecallRequest struct {
	Query  string
	Limit  int
	Scope  string
	Types  []string
	Caller string
}

// RecallResult is the agent_recall response (plan 6.5).
type RecallResult struct {
	Command  string           `json:"command"`
	Scope    string           `json:"scope"`
	Project  string           `json:"project"`
	Banks    []string         `json:"banks"`
	Count    int              `json:"count"`
	Memories []RecalledMemory `json:"memories"`
}

// RecalledMemory is one row of a RecallResult.
type RecalledMemory struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Project     string   `json:"project"`
	Description string   `json:"description"`
	Content     string   `json:"content"`
	Links       []string `json:"links"`
	Timestamp   int64    `json:"timestamp"`
	Score       float64  `json:"score"`
}

// Recall ranks the central memories for req.Query over the banks its scope
// names. The ranking, the type filter and the content_id collapse are all
// applied by memory.RecallFiltered, so the limit counts only kept rows.
func Recall(ctx context.Context, mem *memory.Memory, req RecallRequest) (RecallResult, error) {
	if strings.TrimSpace(req.Query) == "" {
		return RecallResult{}, errors.New("ccmemory: recall: query is required")
	}
	scope := req.Scope
	if scope == "" {
		scope = scopeProject
	}
	if !slices.Contains(validScopes, scope) {
		return RecallResult{}, fmt.Errorf("ccmemory: recall: unknown scope %q; valid scopes: %s", scope, strings.Join(validScopes, ", "))
	}
	for _, t := range req.Types {
		if !slices.Contains(validTypes, t) {
			return RecallResult{}, fmt.Errorf("ccmemory: recall: unknown type %q; valid types: %s", t, strings.Join(validTypes, ", "))
		}
	}
	limit := req.Limit
	if limit <= 0 {
		limit = memory.RecallLimit
	}

	all, err := mem.Banks()
	if err != nil {
		return RecallResult{}, err
	}
	central := slices.DeleteFunc(all, func(b string) bool { return !strings.HasPrefix(b, CentralBankPrefix) })
	if len(central) == 0 {
		return RecallResult{}, errors.New("ccmemory: recall: no central memory banks; run `leankg memory sync` first")
	}

	project, projectBank := "", ""
	if req.Caller != "" && slices.Contains(central, ProjectBank(req.Caller)) {
		project, projectBank = req.Caller, ProjectBank(req.Caller)
	}
	banks := scopeBanks(scope, projectBank, central)
	rows, err := mem.RecallFiltered(ctx, banks, req.Query, limit, rowFilter(req.Types))
	if err != nil {
		return RecallResult{}, err
	}
	res := RecallResult{
		Command:  recallCommand,
		Scope:    scope,
		Project:  project,
		Banks:    banks,
		Count:    len(rows),
		Memories: make([]RecalledMemory, 0, len(rows)),
	}
	for _, e := range rows {
		res.Memories = append(res.Memories, recalledOf(e))
	}
	return res, nil
}

// scopeBanks lists the banks a scope reads, the caller's project bank first:
// project is that bank plus cc-global, global is cc-global, all is every cc-
// bank. projectBank is empty when the caller has no project bank.
func scopeBanks(scope, projectBank string, central []string) []string {
	banks := []string{}
	if projectBank != "" && scope != scopeGlobal {
		banks = append(banks, projectBank)
	}
	for _, b := range central {
		if b != projectBank && (scope == scopeAll || b == GlobalBank) {
			banks = append(banks, b)
		}
	}
	return banks
}

// rowFilter keeps the rows of the requested types (none means all) and drops
// a row whose content_id was already kept. RecallFiltered offers each candidate
// once, in rank order, before the limit, so the first row of a content_id is
// the best-ranked one.
func rowFilter(types []string) func(memory.Entry) bool {
	seen := map[string]bool{}
	return func(e memory.Entry) bool {
		if len(types) > 0 && !slices.Contains(types, metaString(e, "kind")) {
			return false
		}
		id := metaString(e, "content_id")
		if id == "" {
			return true
		}
		if seen[id] {
			return false
		}
		seen[id] = true
		return true
	}
}

// recalledOf maps a bank row to a RecalledMemory (plan 6.4 metadata keys).
func recalledOf(e memory.Entry) RecalledMemory {
	return RecalledMemory{
		ID:          e.ID,
		Name:        metaString(e, "name"),
		Kind:        metaString(e, "kind"),
		Project:     metaString(e, "project"),
		Description: metaString(e, "description"),
		Content:     e.Content,
		Links:       metaStrings(e, "links"),
		Timestamp:   e.Timestamp,
		Score:       e.Score,
	}
}

func metaString(e memory.Entry, key string) string {
	s, _ := e.Metadata[key].(string)
	return s
}

// metaStrings reads a string list from a row decoded from JSON.
func metaStrings(e memory.Entry, key string) []string {
	items, _ := e.Metadata[key].([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		s, _ := it.(string)
		out = append(out, s)
	}
	return out
}
