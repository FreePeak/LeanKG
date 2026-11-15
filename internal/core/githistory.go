package core

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/FreePeak/LeanKG/internal/githistory"
	"github.com/FreePeak/LeanKG/internal/store"
)

// gitImportSlots holds one single-slot semaphore per store (store.Backend ->
// chan struct{}). The history cursor is read and written inside a git import,
// so two imports over one store must not interleave: the second waits here,
// then finds HEAD already stored and reports an incremental no-op. Engines that
// share a store share the slot. Backends are pointers, so they are comparable
// map keys.
var gitImportSlots sync.Map

func gitImportSlot(st store.Backend) chan struct{} {
	slot, _ := gitImportSlots.LoadOrStore(st, make(chan struct{}, 1))
	return slot.(chan struct{})
}

// gitImport records the project's git history (import action "git"). The
// history is read from the project root, the same tree the code index walks.
// The walk, the cursor write and the inventory refresh run under the store's
// git import slot; a caller that cannot get the slot before ctx ends gives up.
// gitImportArgs are the args of import{action:"git"}.
type gitImportArgs struct {
	MaxCommits int `json:"max_commits"`
}

func (e *Engine) gitImport(ctx context.Context, _ ImportRequest, a gitImportArgs) (map[string]any, error) {
	slot := gitImportSlot(e.st)
	select {
	case slot <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-slot }()

	res, err := githistory.Import(ctx, e.st, e.projectRoot(), githistory.Options{
		MaxCommits: a.MaxCommits,
	})
	if err != nil {
		return nil, err
	}
	if _, err := e.refreshInventory(); err != nil {
		return nil, err
	}
	return map[string]any{"git": res}, nil
}

// gitHistoryQuery answers the history, cochange and churn query actions.
// history and cochange take the file path in query (project-relative, or
// absolute inside the project); churn takes args.since as a git date.
// gitHistoryArgs are the args of query{action:"history"|"cochange"|"churn"}.
// since is read by churn only.
type gitHistoryArgs struct {
	envelopeArgs
	Since string `json:"since"`
}

func (e *Engine) gitHistoryQuery(ctx context.Context, req QueryRequest, a gitHistoryArgs) (map[string]any, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	if req.Action == "churn" {
		files, err := githistory.Churn(ctx, e.projectRoot(), githistory.ChurnOptions{
			Limit: limit,
			Since: a.Since,
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"files": files}, nil
	}
	if req.Query == "" {
		return nil, fmt.Errorf("query %s requires query (a file path)", req.Action)
	}
	path := e.relToProject(req.Query)
	if req.Action == "cochange" {
		partners, err := githistory.CoChange(ctx, e.st, path, limit)
		if err != nil {
			return nil, err
		}
		return map[string]any{"path": path, "partners": partners}, nil
	}
	commits, err := githistory.History(ctx, e.st, path, limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": path, "commits": commits}, nil
}

// relToProject maps an absolute path inside the project to its project-relative
// spelling, the form the history rows are keyed by. Other inputs are returned
// unchanged.
func (e *Engine) relToProject(p string) string {
	if !filepath.IsAbs(p) {
		return filepath.ToSlash(p)
	}
	if rel, err := filepath.Rel(e.projectRoot(), p); err == nil {
		return filepath.ToSlash(rel)
	}
	return p
}
