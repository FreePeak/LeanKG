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
	"encoding/json"
	"math"
	"net/http"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// maxTTLSecs is the largest whole-second TTL a time.Duration can hold
// (~292 years). A ttl_secs beyond it would wrap to "never expires".
const maxTTLSecs = math.MaxInt64 / int64(time.Second)

// Routes returns the /api/v1/auth/* handler tree over a store.
func Routes(st store.Backend) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/register", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Email    string `json:"email"`
			Password string `json:"password"`
			Name     string `json:"name"`
		}
		if !decodeBody(w, r, &req) {
			return
		}
		account, err := Register(st, req.Email, req.Password, req.Name)
		if err != nil {
			writeFail(w, err.Error())
			return
		}
		writeOK(w, account)
	})
	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if !decodeBody(w, r, &req) {
			return
		}
		account, err := VerifyLogin(st, req.Email, req.Password)
		if err != nil {
			writeFail(w, err.Error())
			return
		}
		writeOK(w, account)
	})
	mux.HandleFunc("POST /api/v1/auth/token", func(w http.ResponseWriter, r *http.Request) {
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
		// Only writers may issue for another account; a reader may issue for
		// itself. `role` falls back to viewer for an unknown or missing value
		// (Rust Role::from_str(..).unwrap_or(Viewer)).
		if !ctx.CanWrite() && req.AccountID != ctx.AccountID {
			writeFail(w, "insufficient permission to issue token")
			return
		}
		role := "viewer"
		if _, known := RoleFromString(req.Role); known {
			role = req.Role
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
		secret, row, err := Mint(st, MintRequest{
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
	})
	mux.HandleFunc("GET /api/v1/auth/token", func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := caller(st, w, r)
		if !ok {
			return
		}
		// A caller with an account sees only its own tokens (Rust
		// list_tokens(ctx.client_id)); a process-level caller (env token or
		// the no-token local default) has no account to filter by and sees all.
		rows, err := st.TokenList()
		if err != nil {
			writeFail(w, err.Error())
			return
		}
		if ctx.AccountID == "" {
			writeOK(w, rows)
			return
		}
		own := make([]store.Token, 0, len(rows))
		for _, t := range rows {
			if t.AccountID == ctx.AccountID {
				own = append(own, t)
			}
		}
		writeOK(w, own)
	})
	mux.HandleFunc("POST /api/v1/auth/token/revoke", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			TokenID string `json:"token_id"`
		}
		if !decodeBody(w, r, &req) {
			return
		}
		// Rust checked only that an Authorization header was present before
		// revoking any token id. The Go port requires a VALID bearer (every
		// other protected handler does); revocation stays unscoped, matching
		// Rust, so any valid caller may revoke any token id.
		if _, ok := caller(st, w, r); !ok {
			return
		}
		rows, err := st.TokenList()
		if err != nil {
			writeFail(w, err.Error())
			return
		}
		revoked := false
		for _, t := range rows {
			if t.ID != req.TokenID || t.RevokedAt != 0 {
				continue
			}
			if err := st.TokenRevoke(t.ID, time.Now().Unix()); err != nil {
				writeFail(w, err.Error())
				return
			}
			revoked = true
			break
		}
		writeOK(w, revoked)
	})
	mux.HandleFunc("POST /api/v1/auth/org", func(w http.ResponseWriter, r *http.Request) {
		// Rust took a bare JSON string body here (Json(name): Json<String>).
		var name string
		if err := json.NewDecoder(r.Body).Decode(&name); err != nil {
			writeFail(w, "invalid JSON body: "+err.Error())
			return
		}
		ctx, ok := requireAccount(st, w, r)
		if !ok {
			return
		}
		org, err := CreateOrg(st, name, ctx.AccountID)
		if err != nil {
			writeFail(w, err.Error())
			return
		}
		writeOK(w, org)
	})
	mux.HandleFunc("POST /api/v1/auth/org/{org_id}/member", func(w http.ResponseWriter, r *http.Request) {
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
		allowed, err := OrgRoleSufficient(st, orgID, ctx.AccountID, OrgRoleAdmin)
		if err != nil {
			writeFail(w, err.Error())
			return
		}
		if !allowed {
			writeFail(w, "insufficient permission: org admin+ required")
			return
		}
		member, err := AddOrgMember(st, orgID, req.AccountID, req.Role)
		if err != nil {
			writeFail(w, err.Error())
			return
		}
		writeOK(w, member)
	})
	mux.HandleFunc("GET /api/v1/auth/org/{org_id}/members", func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := requireAccount(st, w, r)
		if !ok {
			return
		}
		orgID := r.PathValue("org_id")
		allowed, err := OrgRoleSufficient(st, orgID, ctx.AccountID, OrgRoleViewer)
		if err != nil {
			writeFail(w, err.Error())
			return
		}
		if !allowed {
			writeFail(w, "not an org member")
			return
		}
		members, err := st.OrgMembers(orgID)
		if err != nil {
			writeFail(w, err.Error())
			return
		}
		writeOK(w, members)
	})
	mux.HandleFunc("POST /api/v1/auth/resource/claim", func(w http.ResponseWriter, r *http.Request) {
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
		if err := ClaimResource(st, req.ResourceType, req.ResourceID, ctx.AccountID, req.OrgID); err != nil {
			writeFail(w, err.Error())
			return
		}
		writeOK(w, true)
	})
	return mux
}

// caller resolves the request's auth context, writing the Rust-shaped error
// response itself when the request is not authorized.
func caller(st store.Backend, w http.ResponseWriter, r *http.Request) (AuthContext, bool) {
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
func requireAccount(st store.Backend, w http.ResponseWriter, r *http.Request) (AuthContext, bool) {
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

// apiResponse is the Rust ApiResponse<T> wire shape: all three keys are always
// present, with null for the unset one.
type apiResponse struct {
	Success bool    `json:"success"`
	Data    any     `json:"data"`
	Error   *string `json:"error"`
}

func writeOK(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: data})
}

func writeFail(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, apiResponse{Success: false, Error: &msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func decodeBody(w http.ResponseWriter, r *http.Request, into any) bool {
	if err := json.NewDecoder(r.Body).Decode(into); err != nil {
		writeFail(w, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}
