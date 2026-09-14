// Portfolio scope in the query surface (issue #376): query{action:"portfolio"}
// fans one read out across the projects in the server-side registry and merges
// the answers with project attribution.
//
// The split of work is deliberate. internal/portfolioreg owns the policy — the
// registry rows, the bounded hot set, the merge, the T0 manifest, the
// per-child error rule — because that is what makes the policy testable without
// a serving stack. THIS file owns the one thing only core can do: run the real
// L0–L3 ladder inside one project. The ladder is never re-implemented here; a
// child is a plain *Engine over the child's read-only store, so a portfolio hit
// is the same shaped object a single-project query returns, with "project" and
// "project_dir" added by the merge.
//
// Read-only by construction: children open with store.RO (sqlite query_only,
// Postgres default_transaction_read_only), so a fleet query can never migrate,
// index, or write. A registered project with no store is a per-child error
// entry in the answer, never a dropped child and never a background index job.
package core

import (
	"context"
	"fmt"

	"github.com/FreePeak/LeanKG/internal/portfolioreg"
	"github.com/FreePeak/LeanKG/internal/store"
)

// PortfolioSummaryAction is the args.cmd value that turns a portfolio query
// into the T0 manifest (registry rows + on-disk store presence, no store
// opens, no indexing).
const PortfolioSummaryAction = "summary"

// portfolioHotSet is the process-wide T1 recency list. One serving process
// fronts one deployment's registry, so one list is the right granularity — and
// it cannot live on Engine, which is per-project.
//
// CEILING: a second registry in the same process (a dashboard pointed at two
// Postgres databases) would share one list; the upgrade path is keying this by
// the resolved registry location.
var portfolioHotSet = portfolioreg.NewHotSet(0)

// portfolioOptions resolves the registry location for this engine: the backend
// the engine is ALREADY talking to (the store handle, not the environment —
// the handle is the authority on which engine won the selection at open time).
// The Postgres dsn is deliberately left empty: portfolioreg.Options resolves it
// per call in cmd's own precedence (LEANKG_PG_URL, then the project's
// leankg.yaml db: block), so the fleet follows whatever the server was started
// with instead of a second copy of that policy.
func (e *Engine) portfolioOptions() portfolioreg.Options {
	return portfolioreg.Options{Engine: e.st.Engine()}
}

// portfolioQuery serves query{action:"portfolio"}.
//
// args.cmd = "summary" answers the T0 manifest and needs no query text;
// anything else is the T1 fan-out, which does. args.action picks the child
// action (default: each child's own ladder router); "portfolio" is refused — a
// fleet query must not recurse into a fleet query.
func (e *Engine) portfolioQuery(ctx context.Context, req QueryRequest) (map[string]any, error) {
	opts := e.portfolioOptions()
	if argStr(req.Args, "cmd") == PortfolioSummaryAction {
		entries, err := portfolioreg.Manifest(ctx, opts, portfolioHotSet)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"action":     "portfolio",
			"cmd":        PortfolioSummaryAction,
			"tier":       "T0 — registry rows and on-disk store presence; no project store was opened",
			"registry":   registryWhere(opts),
			"hot_limit":  portfolioHotSet.Limit(),
			"projects":   entries,
			"count":      len(entries),
			"registered": len(entries),
		}, nil
	}
	if req.Query == "" {
		return nil, fmt.Errorf("query portfolio requires query text (or args.cmd %q for the T0 manifest)", PortfolioSummaryAction)
	}
	childAction := argStr(req.Args, "action")
	if childAction == PortfolioAction {
		return nil, fmt.Errorf("query portfolio cannot fan out to another portfolio query (args.action %q)", childAction)
	}
	rep, err := portfolioreg.FanOut(ctx, opts, portfolioreg.Query{
		Text: req.Query, Action: childAction, Limit: req.Limit, Args: req.Args,
	}, portfolioHotSet, e.portfolioChild)
	if err != nil {
		return nil, err
	}
	resp := map[string]any{
		"action":               PortfolioAction,
		"query":                rep.Query,
		"tier":                 "T1 — hot set opened read-only; nothing was indexed",
		"registry":             registryWhere(opts),
		"hot_limit":            rep.HotLimit,
		"projects_registered":  rep.Registered,
		"projects_served":      rep.Served,
		"projects_failed":      rep.Failed,
		"projects_not_hot":     rep.NotHot,
		"projects_not_indexed": rep.NotIndexed,
		"children":             rep.Projects,
		"hits":                 rep.Hits,
	}
	if rep.Action != "" {
		resp["child_action"] = rep.Action
	}
	if rep.Registered == 0 {
		resp["guidance"] = "No projects in the portfolio registry: `leankg register-project <dir> --name <n>` (or `leankg index`, which registers on completion)."
	}
	return resp, nil
}

// portfolioChild is the per-project executor: open the project read-only, build
// an Engine over it exactly like the serving transports build one, and run the
// caller's query through the normal ladder. The handle closes when the child
// answer is in hand, so a fan-out never accumulates open stores.
func (e *Engine) portfolioChild(ctx context.Context, p store.ProjectRecord, q portfolioreg.Query) (map[string]any, error) {
	st, err := e.portfolioOptions().OpenChild(ctx, p.Dir)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	child := New(st, nil, e.embedder)
	child.SetProjectDir(p.Dir)
	if e.langsReg != nil {
		// Reuse the parent's already-activated language registry: activation is
		// per grammar pack, not per project, and a child must not re-probe
		// grammars the serving process has already resolved.
		child.SetLangsRegistry(e.langsReg)
	}
	return child.Query(ctx, QueryRequest{
		Action: q.Action, Query: q.Text, Limit: q.Limit, Args: q.Args,
	})
}

// registryWhere names the registry a portfolio answer came from, so an
// operator reading a result can tell which fleet they looked at. The dsn is
// never echoed — store.Path() redacts credentials for exactly this reason.
func registryWhere(o portfolioreg.Options) string {
	if o.Engine == store.EnginePostgres {
		return "postgres portfolio registry (its own schema in the shared database)"
	}
	return "sqlite portfolio registry at " + portfolioreg.DBPath()
}

// PortfolioAction is the query-tool action served here — the same string
// core.go's switch dispatches on, and the value the fan-out refuses as a child
// action so a fleet query can never recurse into itself.
const PortfolioAction = portfolioreg.PortfolioAction
