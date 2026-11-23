// HTTP surface for the enterprise auth model (Rust src/api/auth_handlers.rs
// parity): registration, login, access-token issuance/listing/revocation, org
// management, org members and resource ownership.
//
// Mount on the REST mux under /api/v1/auth/:
//
//	mux.Handle("/api/v1/auth/", auth.Routes(st))
//
// Responses keep the Rust ApiResponse envelope — {"success", "data", "error"}
// with the error path at HTTP 400 — so existing clients port unchanged.
package auth

import (
	"errors"
	"log"
	"math"
	"net/http"
	"time"

	"github.com/FreePeak/LeanKG/internal/httpx"
	"github.com/FreePeak/LeanKG/internal/store"
)

// maxTTLSecs is the largest whole-second TTL a time.Duration can hold
// (~292 years). A ttl_secs beyond it would wrap to "never expires".
const maxTTLSecs = math.MaxInt64 / int64(time.Second)

// registrationFailedMsg is the one generic text a refused registration returns
// (#586). It is the same for a duplicate email and any other refusal the
// handler does not pass through.
const registrationFailedMsg = "registration failed"

// Routes returns the /api/v1/auth/* handler tree over a store. Every route is
// Public: the bootstrap is mounted outside the listener gate, and each handler
// resolves its own caller (caller, requireAccount), so its failures keep the
// envelope's 400 shape instead of the gate's 401.
func Routes(st Store) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST /api/v1/auth/register", Public(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Email    string `json:"email"`
			Password string `json:"password"`
			Name     string `json:"name"`
		}
		if !decodeBody(w, r, &req) {
			return
		}
		account, err := Register(r.Context(), st, req.Email, req.Password, req.Name)
		if errors.Is(err, ErrAccountExists) {
			// Generic on purpose (#586): a duplicate answers like any other
			// registration failure, so the response never reveals an existing email.
			writeFail(w, registrationFailedMsg)
			return
		}
		if err != nil {
			// A store failure is a server fault: log the cause and answer generically,
			// as the dashboard does, so no driver text reaches the client (#601 item 6).
			if status := httpx.StatusFor(err); status >= http.StatusInternalServerError {
				log.Printf("auth: register: %v", err)
				msg := "internal error"
				httpx.WriteJSON(w, status, apiResponse{Success: false, Error: &msg})
				return
			}
			writeFail(w, err.Error())
			return
		}
		writeOK(w, account)
	})))
	mux.Handle("POST /api/v1/auth/login", Public(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if !decodeBody(w, r, &req) {
			return
		}
		account, err := VerifyLogin(r.Context(), st, req.Email, req.Password)
		if err != nil {
			writeFail(w, err.Error())
			return
		}
		writeOK(w, account)
	})))
	mux.Handle("POST /api/v1/auth/token", Public(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AccountID string `json:"account_id"`
			OrgID     string `json:"org_id"`
			Role      string `json:"role"`
			Name      string `json:"name"`
			TTLSecs   *int64 `json:"ttl_secs"`
		}
		if !decodeBody(w, r, &req) {
			return
		}
		ctx, ok := caller(st, w, r)
		if !ok {
			return
		}
		// Issuing for ANOTHER account is an admin action; issuing for yourself
		// needs only a valid bearer. (`role` falls back to viewer for an unknown
		// or missing value — Rust Role::from_str(..).unwrap_or(Viewer).)
		if req.AccountID != ctx.AccountID && !ctx.CanAdmin() {
			writeFail(w, "insufficient permission to issue a token for another account (admin required)")
			return
		}
		// A process-level caller (env token) has no account to own the minted
		// token, so only one that can write may issue (#601 B2).
		if ctx.AccountID == "" && !ctx.CanWrite() {
			msg := "insufficient permission to issue a token: write access required"
			httpx.WriteJSON(w, http.StatusForbidden, apiResponse{Success: false, Error: &msg})
			return
		}
		role := "viewer"
		if _, known := RoleFromString(req.Role); known {
			role = req.Role
		}
		// Privilege ceiling for EVERY caller: nobody mints above their own role.
		// Guarding only non-writers was not enough — a contributor bearer could
		// still mint itself admin. The Rust handler had the same hole while its
		// doc comment promised admin-only issuance.
		if want, known := RoleFromString(role); !known || want > ctx.Role {
			role = ctx.Role.String()
		}
		// ttl_secs is client input: convert through time.Duration only within
		// its whole-second range. Beyond it the multiply wraps and a token
		// meant to expire could be minted as never-expiring (TTL 0).
		var ttl time.Duration
		if req.TTLSecs != nil {
			secs := *req.TTLSecs
			if secs > maxTTLSecs || secs < -maxTTLSecs {
				writeFail(w, "ttl_secs out of range")
				return
			}
			ttl = time.Duration(secs) * time.Second
		}
		secret, row, err := Mint(r.Context(), st, MintRequest{
			Name:      req.Name,
			Role:      role,
			AccountID: req.AccountID,
			OrgID:     req.OrgID,
			TTL:       ttl,
		})
		if err != nil {
			writeFail(w, err.Error())
			return
		}
		issued := struct {
			AccessToken string `json:"access_token"`
			TokenType   string `json:"token_type"`
			AccountID   string `json:"account_id"`
			Role        string `json:"role"`
			ExpiresAt   *int64 `json:"expires_at"`
		}{AccessToken: secret, TokenType: "Bearer", AccountID: row.AccountID, Role: row.Role}
		if row.ExpiresAt != 0 {
			issued.ExpiresAt = &row.ExpiresAt
		}
		writeOK(w, issued)
	})))
	mux.Handle("GET /api/v1/auth/token", Public(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := caller(st, w, r)
		if !ok {
			return
		}
		rows, err := st.TokenList(r.Context())
		if err != nil {
			writeFail(w, err.Error())
			return
		}
		visible := make([]tokenSummary, 0, len(rows))
		for _, t := range rows {
			if tokenVisibleTo(ctx, t) {
				visible = append(visible, summarizeToken(t))
			}
		}
		writeOK(w, visible)
	})))
	mux.Handle("POST /api/v1/auth/token/revoke", Public(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			TokenID string `json:"token_id"`
		}
		if !decodeBody(w, r, &req) {
			return
		}
		// Revoking needs a valid bearer and either admin rights or ownership of
		// the token (CX-03). Rust checked only for an Authorization header, so
		// any bearer could revoke any token id.
		ctx, ok := caller(st, w, r)
		if !ok {
			return
		}
		rows, err := st.TokenList(r.Context())
		if err != nil {
			writeFail(w, err.Error())
			return
		}
		revoked := false
		for _, t := range rows {
			if t.ID != req.TokenID {
				continue
			}
			if !ctx.CanAdmin() && (ctx.AccountID == "" || t.AccountID != ctx.AccountID) {
				msg := "insufficient permission to revoke another account's token (owner or admin required)"
				httpx.WriteJSON(w, http.StatusForbidden, apiResponse{Success: false, Error: &msg})
				return
			}
			if t.RevokedAt == 0 {
				if err := st.TokenRevoke(r.Context(), t.ID, time.Now().Unix()); err != nil {
					writeFail(w, err.Error())
					return
				}
				revoked = true
			}
			break
		}
		writeOK(w, revoked)
	})))
	mux.Handle("POST /api/v1/auth/org", Public(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Rust took a bare JSON string body here (Json(name): Json<String>).
		var name string
		if err := httpx.DecodeJSON(w, r, &name); err != nil {
			writeDecodeFail(w, err)
			return
		}
		ctx, ok := requireAccount(st, w, r)
		if !ok {
			return
		}
		org, err := CreateOrg(r.Context(), st, name, ctx.AccountID)
		if err != nil {
			writeFail(w, err.Error())
			return
		}
		writeOK(w, org)
	})))
	mux.Handle("POST /api/v1/auth/org/{org_id}/member", Public(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AccountID string `json:"account_id"`
			Role      string `json:"role"`
		}
		if !decodeBody(w, r, &req) {
			return
		}
		ctx, ok := requireAccount(st, w, r)
		if !ok {
			return
		}
		orgID := r.PathValue("org_id")
		allowed, err := OrgRoleSufficient(r.Context(), st, orgID, ctx.AccountID, OrgRoleAdmin)
		if err != nil {
			writeFail(w, err.Error())
			return
		}
		if !allowed {
			writeFail(w, "insufficient permission: org admin+ required")
			return
		}
		member, err := AddOrgMember(r.Context(), st, orgID, req.AccountID, req.Role)
		if err != nil {
			writeFail(w, err.Error())
			return
		}
		writeOK(w, member)
	})))
	mux.Handle("GET /api/v1/auth/org/{org_id}/members", Public(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := requireAccount(st, w, r)
		if !ok {
			return
		}
		orgID := r.PathValue("org_id")
		allowed, err := OrgRoleSufficient(r.Context(), st, orgID, ctx.AccountID, OrgRoleViewer)
		if err != nil {
			writeFail(w, err.Error())
			return
		}
		if !allowed {
			writeFail(w, "not an org member")
			return
		}
		members, err := st.OrgMembers(r.Context(), orgID)
		if err != nil {
			writeFail(w, err.Error())
			return
		}
		writeOK(w, members)
	})))
	mux.Handle("POST /api/v1/auth/resource/claim", Public(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ResourceType string `json:"resource_type"`
			ResourceID   string `json:"resource_id"`
			OrgID        string `json:"org_id"`
		}
		if !decodeBody(w, r, &req) {
			return
		}
		ctx, ok := requireAccount(st, w, r)
		if !ok {
			return
		}
		if err := ClaimResource(r.Context(), st, req.ResourceType, req.ResourceID, ctx.AccountID, req.OrgID); err != nil {
			writeFail(w, err.Error())
			return
		}
		writeOK(w, true)
	})))
	return mux
}

// caller resolves the request's auth context, writing the Rust-shaped error
// response itself when the request is not authorized.
func caller(st Store, w http.ResponseWriter, r *http.Request) (AuthContext, bool) {
	ctx, err := Resolve(st, r)
	if err != nil {
		writeFail(w, err.Error())
		return AuthContext{}, false
	}
	return ctx, true
}

// requireAccount is caller plus the requirement that the caller resolved to an
// account: org and ownership endpoints attribute their writes to it, so a
// process-level caller (env token, or the no-token local default) cannot use
// them.
func requireAccount(st Store, w http.ResponseWriter, r *http.Request) (AuthContext, bool) {
	ctx, ok := caller(st, w, r)
	if !ok {
		return AuthContext{}, false
	}
	if ctx.AccountID == "" {
		writeFail(w, "account-scoped endpoint requires an account access token")
		return AuthContext{}, false
	}
	return ctx, true
}

// tokenSummary is the token-list wire shape: store.Token without the at-rest
// hash. The hash is never serialized to a client (CX-03), the same rule that
// keeps password_hash off the account response.
type tokenSummary struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Role       string   `json:"role"`
	CreatedAt  int64    `json:"created_at"`
	AccountID  string   `json:"account_id,omitempty"`
	OrgID      string   `json:"org_id,omitempty"`
	Scopes     []string `json:"scopes,omitempty"`
	ExpiresAt  int64    `json:"expires_at,omitempty"`
	RevokedAt  int64    `json:"revoked_at,omitempty"`
	LastUsedAt int64    `json:"last_used_at,omitempty"`
}

// tokenVisibleTo is the token-list rule. A caller with an account sees only its
// own tokens (Rust list_tokens(ctx.client_id)). A process-level caller (an env
// token, or a DB token with no account) has no account to filter by, so it sees
// the full list only when it is an admin; below admin it sees nothing (#586).
func tokenVisibleTo(ctx AuthContext, t store.Token) bool {
	if ctx.AccountID == "" {
		return ctx.CanAdmin()
	}
	return t.AccountID == ctx.AccountID
}

func summarizeToken(t store.Token) tokenSummary {
	return tokenSummary{
		ID: t.ID, Name: t.Name, Role: t.Role, CreatedAt: t.CreatedAt,
		AccountID: t.AccountID, OrgID: t.OrgID, Scopes: t.Scopes,
		ExpiresAt: t.ExpiresAt, RevokedAt: t.RevokedAt, LastUsedAt: t.LastUsedAt,
	}
}

// apiResponse is the Rust ApiResponse<T> wire shape: all three keys are always
// present, with null for the unset one.
type apiResponse struct {
	Success bool    `json:"success"`
	Data    any     `json:"data"`
	Error   *string `json:"error"`
	Code    string  `json:"code,omitempty"`
}

func writeOK(w http.ResponseWriter, data any) {
	httpx.WriteJSON(w, http.StatusOK, apiResponse{Success: true, Data: data})
}

func writeFail(w http.ResponseWriter, msg string) {
	httpx.WriteJSON(w, http.StatusBadRequest, apiResponse{Success: false, Error: &msg})
}

func decodeBody(w http.ResponseWriter, r *http.Request, into any) bool {
	if err := httpx.DecodeJSON(w, r, into); err != nil {
		writeDecodeFail(w, err)
		return false
	}
	return true
}

// writeDecodeFail answers a body that did not decode: 413 when it is over the
// size cap, 400 otherwise, in the envelope with the catalog code (#601 item 5).
func writeDecodeFail(w http.ResponseWriter, err error) {
	msg := err.Error()
	httpx.WriteJSON(w, httpx.StatusFor(err), apiResponse{Success: false, Error: &msg, Code: httpx.CodeOf(err)})
}
