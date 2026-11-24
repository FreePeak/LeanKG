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
	"errors"
	"fmt"
	"path/filepath"

	"github.com/FreePeak/LeanKG/internal/auth"
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
func (e *Engine) portfolioQuery(ctx context.Context, req QueryRequest, a portfolioArgs) (map[string]any, error) {
	if e.hosted {
		return nil, fmt.Errorf("portfolio queries are disabled in hosted mode: name one project")
	}
	opts := e.portfolioOptions()
	grant, _ := auth.AuthFrom(ctx)
	cmd, alias := subCommandKey(a.Command, a.Cmd)
	if cmd == PortfolioSummaryAction {
		registered, err := portfolioreg.Projects(ctx, opts)
		if errors.Is(err, portfolioreg.ErrNoRegistry) && grant.ProjectRestricted() {
			// The error text names the registry path, which a restricted caller
			// must not see. It gets the empty manifest the fan-out answers instead.
			registered, err = nil, nil
		}
		if err != nil {
			return nil, err
		}
		visible, withheld := portfolioreg.Split(registered, grantedProjects(grant))
		entries := portfolioreg.ManifestOf(opts, visible, portfolioHotSet)
		out := map[string]any{
			"action":            "portfolio",
			"cmd":               PortfolioSummaryAction,
			"tier":              "T0 — registry rows and on-disk store presence; no project store was opened",
			"hot_limit":         portfolioHotSet.Limit(),
			"projects":          entries,
			"count":             len(entries),
			"registered":        len(entries),
			"projects_withheld": withheld,
		}
		if !grant.ProjectRestricted() {
			out["registry"] = registryWhere(opts)
		}
		if alias {
			out["deprecated"] = deprecatedSubCommandNote
		}
		return out, nil
	}
	if req.Query == "" {
		return nil, fmt.Errorf("query portfolio requires query text (or args.cmd %q for the T0 manifest)", PortfolioSummaryAction)
	}
	if a.Action == PortfolioAction {
		return nil, fmt.Errorf("query portfolio cannot fan out to another portfolio query (args.action %q)", a.Action)
	}
	rep, err := portfolioreg.FanOut(ctx, opts, portfolioreg.Query{
		Text: req.Query, Action: a.Action, Limit: req.Limit, Args: childArgs(req.Args),
		Visible: grantedProjects(grant),
	}, portfolioHotSet, e.portfolioChild)
	if err != nil {
		return nil, err
	}
	resp := map[string]any{
		"action":               PortfolioAction,
		"query":                rep.Query,
		"tier":                 "T1 — hot set opened read-only; nothing was indexed",
		"hot_limit":            rep.HotLimit,
		"projects_registered":  rep.Registered,
		"projects_served":      rep.Served,
		"projects_failed":      rep.Failed,
		"projects_not_hot":     rep.NotHot,
		"projects_not_indexed": rep.NotIndexed,
		"projects_withheld":    rep.Withheld,
		"children":             rep.Projects,
		"hits":                 rep.Hits,
	}
	if !grant.ProjectRestricted() {
		resp["registry"] = registryWhere(opts)
	}
	if rep.Action != "" {
		resp["child_action"] = rep.Action
	}
	if alias {
		resp["deprecated"] = deprecatedSubCommandNote
	}
	if rep.Registered == 0 {
		// Withheld projects are not in Registered, so a restricted caller whose
		// projects were all withheld would otherwise be told the registry is empty.
		if rep.Withheld > 0 {
			resp["guidance"] = "No project is visible to this token."
		} else {
			resp["guidance"] = "No projects in the portfolio registry: `leankg register-project <dir> --name <n>` (or `leankg index`, which registers on completion)."
		}
	}
	return resp, nil
}

// grantedProjects applies the caller's project grant to the registry. A caller
// with no grant (stdio, a local listener) sees every project.
func grantedProjects(grant auth.AuthContext) func(store.ProjectRecord, bool) bool {
	return func(p store.ProjectRecord, shared bool) bool {
		return grant.ProjectAllowed(filepath.Base(p.Dir), shared)
	}
}

// portfolioArgs are the args of query{action:"portfolio"}. command (cmd is its
// deprecated alias) picks the manifest; action picks the child action.
type portfolioArgs struct {
	envelopeArgs
	Action  string `json:"action"`
	Command string `json:"command"`
	Cmd     string `json:"cmd"`
}

// childArgs is the args a child query receives: the parent's own keys (the
// child action and the sub-command) are consumed here and not passed down.
func childArgs(args map[string]any) map[string]any {
	out := make(map[string]any, len(args))
	for k, v := range args {
		switch k {
		case "action", "command", "cmd":
			continue
		}
		out[k] = v
	}
	return out
}

// portfolioChild is the per-project executor: open the project read-only, build
// an Engine over it exactly like the serving transports build one, and run the
// caller's query through the normal ladder. The handle closes when the child
// answer is in hand, so a fan-out never accumulates open stores.
func (e *Engine) portfolioChild(ctx context.Context, p store.ProjectRecord, q portfolioreg.Query) (map[string]any, error) {
	st, err := store.OpenBackend(ctx, e.portfolioOptions().ChildOpenOptions(p.Dir))
	if err != nil {
		return nil, err
	}
	defer st.Close()
	// The parent's already-activated language registry is reused: activation is
	// per grammar pack, not per project, and a child must not re-probe grammars
	// the serving process has already resolved.
	child := New(st, Options{Embedder: e.embedder, ProjectDir: p.Dir, Langs: e.langsReg})
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
