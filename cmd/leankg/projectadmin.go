package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/httpx"
	"github.com/FreePeak/LeanKG/internal/portfolioreg"
	"github.com/FreePeak/LeanKG/internal/projects"
)

// projectAdmin is the hosted control plane's project registration on the REST
// listener. POST /api/v1/projects {dir, org_id} adds a project to the running
// router, claims it for the org (a ResourceClaim in the home store, which is
// what org-keyed tokens are scoped by) and upserts the portfolio registry.
// DELETE /api/v1/projects {dir} undoes all three. The two stores stay in step:
// a failed later step rolls the earlier ones back.
type projectAdmin struct {
	router   *projects.Router
	st       auth.Store
	registry portfolioreg.Options
}

// projectAdminRoute mounts projectAdmin behind the bearer gate and the Admin
// role. It is the one declaration of the route's access.
func projectAdminRoute(st auth.Store, router *projects.Router, registry portfolioreg.Options) http.Handler {
	return auth.Public(auth.MiddlewareWithStore(st, auth.Require(auth.Admin, projectAdmin{
		router:   router,
		st:       st,
		registry: registry,
	})))
}

type projectAdminBody struct {
	Dir   string `json:"dir"`
	OrgID string `json:"org_id"`
}

func (a projectAdmin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body projectAdminBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		httpx.WriteMessage(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	switch r.Method {
	case http.MethodPost:
		a.register(w, r, body)
	case http.MethodDelete:
		a.unregister(w, r, body)
	default:
		w.Header().Set("Allow", "POST, DELETE")
		httpx.WriteMessage(w, http.StatusMethodNotAllowed, "use POST to register or DELETE to unregister a project")
	}
}

func (a projectAdmin) register(w http.ResponseWriter, r *http.Request, body projectAdminBody) {
	if body.Dir == "" || body.OrgID == "" {
		httpx.WriteMessage(w, http.StatusBadRequest, "dir and org_id are required")
		return
	}
	dir, err := portfolioreg.Canonical(body.Dir)
	if err != nil {
		httpx.WriteMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		httpx.WriteMessage(w, http.StatusBadRequest, "dir must be an existing project directory")
		return
	}
	ctx := r.Context()
	orgs, err := a.st.ResourceOrgs(ctx, auth.ResourceProject, dir)
	if err != nil {
		httpx.WriteMessage(w, http.StatusInternalServerError, "project claim lookup failed")
		return
	}
	for _, org := range orgs {
		if org != body.OrgID {
			// One project belongs to one org: a second claim would show it to both.
			httpx.WriteMessage(w, http.StatusConflict, "project is already claimed by another org")
			return
		}
	}
	if err := auth.ClaimResource(ctx, a.st, auth.ResourceProject, dir, orgOwner(body.OrgID), body.OrgID); err != nil {
		httpx.WriteMessage(w, http.StatusInternalServerError, "project claim failed")
		return
	}
	if err := a.router.Add(dir); err != nil {
		a.rollbackClaim(ctx, dir)
		httpx.WriteMessage(w, http.StatusInternalServerError, "project registration failed")
		return
	}
	// Registration keeps an existing row's name; the counts are the totals a
	// real index run writes later, so a fresh registration reports zero.
	if _, err := portfolioreg.Register(ctx, a.registry, dir, "", 0, 0, nil); err != nil {
		_ = a.router.Remove(dir)
		a.rollbackClaim(ctx, dir)
		httpx.WriteMessage(w, http.StatusInternalServerError, "portfolio registry update failed")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"dir": dir, "org_id": body.OrgID, "registered": true})
}

func (a projectAdmin) unregister(w http.ResponseWriter, r *http.Request, body projectAdminBody) {
	if body.Dir == "" {
		httpx.WriteMessage(w, http.StatusBadRequest, "dir is required")
		return
	}
	dir, err := portfolioreg.Canonical(body.Dir)
	if err != nil {
		httpx.WriteMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.router.Remove(dir); err != nil {
		httpx.WriteMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := r.Context()
	if err := a.st.ResourceRelease(ctx, auth.ResourceProject, dir); err != nil {
		httpx.WriteMessage(w, http.StatusInternalServerError, "project claim release failed")
		return
	}
	removed, err := portfolioreg.Forget(ctx, a.registry, dir)
	if err != nil {
		httpx.WriteMessage(w, http.StatusInternalServerError, "portfolio registry update failed")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"dir": dir, "removed": removed})
}

// rollbackClaim drops the claim a failed registration left behind. The store
// error is not reported: the caller already answers with the failure.
func (a projectAdmin) rollbackClaim(ctx context.Context, dir string) {
	_ = a.st.ResourceRelease(ctx, auth.ResourceProject, dir)
}

// restoreClaimedProjects re-registers every project the home store claims, so a
// restarted hosted server routes them again without an admin re-registering each
// one. The claims are the source of truth: DELETE releases a claim, so a deleted
// project is not restored. It does nothing outside hosted mode.
func restoreClaimedProjects(ctx context.Context, st auth.Store, router *projects.Router) error {
	if !router.Hosted() {
		return nil
	}
	claimed, err := st.ClaimedResourceIDs(ctx, auth.ResourceProject)
	if err != nil {
		return fmt.Errorf("list project claims: %w", err)
	}
	n := router.Restore(ctx, claimed)
	log.Printf("hosted mode: restored %d of %d claimed project(s)", n, len(claimed))
	return nil
}

// orgOwner is the owner label of an org-level project claim. It is not an
// account id, so no account is ever the owner of a project claimed this way;
// the org_id column is what scopes visibility.
func orgOwner(orgID string) string { return "org:" + orgID }
