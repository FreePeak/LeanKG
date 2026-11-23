package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/errs"
	"github.com/FreePeak/LeanKG/internal/store"
)

// newAuthServer mounts Routes exactly as the REST integrator does, so the tests
// exercise the documented mount hunk (subtree mount, full paths inside).
func newAuthServer(t *testing.T, st store.Backend) http.Handler {
	t.Helper()
	parent := http.NewServeMux()
	parent.Handle("/api/v1/auth/", Routes(st))
	return parent
}

// doJSON issues one request and decodes the Rust-shaped ApiResponse envelope.
func doJSON(t *testing.T, h http.Handler, method, path, bearer string, body any) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var decoded map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("%s %s: response is not JSON (%d): %s", method, path, rec.Code, rec.Body.String())
	}
	for _, key := range []string{"success", "data", "error"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("%s %s: envelope missing %q key: %v", method, path, key, decoded)
		}
	}
	return rec.Code, decoded
}

func dataMap(t *testing.T, decoded map[string]any) map[string]any {
	t.Helper()
	data, ok := decoded["data"].(map[string]any)
	if !ok {
		t.Fatalf("data is not an object: %v", decoded["data"])
	}
	return data
}

func errString(t *testing.T, decoded map[string]any) string {
	t.Helper()
	msg, ok := decoded["error"].(string)
	if !ok {
		t.Fatalf("error is not a string: %v", decoded["error"])
	}
	return msg
}

// TestAuthRoutesHappyPath walks the ported Rust handler flow end to end:
// register -> login -> issue -> list -> create org -> add/list members -> claim.
func TestAuthRoutesHappyPath(t *testing.T) {
	clearTokenEnv(t)
	st := openAuthStore(t)
	h := newAuthServer(t, st)

	code, resp := doJSON(t, h, "POST", "/api/v1/auth/register", "", map[string]string{
		"email": "api@example.com", "password": "password123", "name": "Api",
	})
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("register = %d %v", code, resp)
	}
	account := dataMap(t, resp)
	if account["email"] != "api@example.com" || account["status"] != "active" {
		t.Fatalf("registered account = %v", account)
	}
	// Security deviation from the Rust port (which serialized the field): a
	// password verifier is credential material and must never leave the
	// process. Pinned here so a future wire-type change cannot reintroduce it.
	if _, ok := account["password_hash"]; ok {
		t.Fatalf("account response leaks password_hash: %v", account)
	}
	accountID, _ := account["id"].(string)

	code, resp = doJSON(t, h, "POST", "/api/v1/auth/login", "", map[string]string{
		"email": "API@example.com", "password": "password123",
	})
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("login = %d %v", code, resp)
	}

	orgs, err := st.OrgsByOwner(context.Background(), accountID)
	if err != nil || len(orgs) != 1 {
		t.Fatalf("bootstrap orgs = %+v, %v", orgs, err)
	}
	bootstrapOrg := orgs[0].ID

	// No token is configured yet, so issuing is the documented local bootstrap
	// path (open mode = admin with no account requirement for self-issue).
	code, resp = doJSON(t, h, "POST", "/api/v1/auth/token", "", map[string]any{
		"account_id": accountID, "org_id": bootstrapOrg, "role": "contributor",
		"name": "ci", "ttl_secs": 3600,
	})
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("issue token = %d %v", code, resp)
	}
	issued := dataMap(t, resp)
	accessToken, _ := issued["access_token"].(string)
	if !strings.HasPrefix(accessToken, "lkg_") || issued["token_type"] != "Bearer" {
		t.Fatalf("issued token = %v", issued)
	}
	if exp, ok := issued["expires_at"].(float64); !ok || exp <= 0 {
		t.Fatalf("expires_at = %v; want a positive epoch for ttl_secs=3600", issued["expires_at"])
	}

	// From here on a bearer is required (tokens exist).
	code, resp = doJSON(t, h, "GET", "/api/v1/auth/token", accessToken, nil)
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("list tokens = %d %v", code, resp)
	}
	rows, ok := resp["data"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("token list = %v; want exactly the caller's token", resp["data"])
	}
	if first, _ := rows[0].(map[string]any); first["account_id"] != accountID {
		t.Fatalf("listed token = %v; want account %s", rows[0], accountID)
	}

	// create org takes a bare JSON string body (Rust Json<String>).
	code, resp = doJSON(t, h, "POST", "/api/v1/auth/org", accessToken, "Second org")
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("create org = %d %v", code, resp)
	}
	org := dataMap(t, resp)
	if org["owner_account_id"] != accountID || org["name"] != "Second org" {
		t.Fatalf("created org = %v", org)
	}
	orgID, _ := org["id"].(string)

	// A second account joins the org through the admin+ endpoint.
	code, resp = doJSON(t, h, "POST", "/api/v1/auth/register", "", map[string]string{
		"email": "peer@example.com", "password": "password123", "name": "Peer",
	})
	if code != http.StatusOK {
		t.Fatalf("register peer = %d %v", code, resp)
	}
	peerID, _ := dataMap(t, resp)["id"].(string)

	code, resp = doJSON(t, h, "POST", "/api/v1/auth/org/"+orgID+"/member", accessToken, map[string]string{
		"account_id": peerID, "role": "member",
	})
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("add member = %d %v", code, resp)
	}
	if member := dataMap(t, resp); member["role"] != "member" || member["account_id"] != peerID {
		t.Fatalf("added member = %v", member)
	}

	code, resp = doJSON(t, h, "GET", "/api/v1/auth/org/"+orgID+"/members", accessToken, nil)
	if code != http.StatusOK {
		t.Fatalf("list members = %d %v", code, resp)
	}
	if members, ok := resp["data"].([]any); !ok || len(members) != 2 {
		t.Fatalf("members = %v; want owner + peer", resp["data"])
	}

	code, resp = doJSON(t, h, "POST", "/api/v1/auth/resource/claim", accessToken, map[string]any{
		"resource_type": "knowledge", "resource_id": "entry-1", "org_id": orgID,
	})
	if code != http.StatusOK || resp["data"] != true {
		t.Fatalf("claim = %d %v", code, resp)
	}
	if owned, err := st.IsResourceOwner(context.Background(), "knowledge", "entry-1", accountID); err != nil || !owned {
		t.Fatalf("ownership not recorded: %v, %v", owned, err)
	}
}

func TestAuthRoutesDenied(t *testing.T) {
	clearTokenEnv(t)
	st := openAuthStore(t)
	h := newAuthServer(t, st)

	register := func(email string) string {
		t.Helper()
		code, resp := doJSON(t, h, "POST", "/api/v1/auth/register", "", map[string]string{
			"email": email, "password": "password123", "name": email,
		})
		if code != http.StatusOK {
			t.Fatalf("register %s = %d %v", email, code, resp)
		}
		id, _ := dataMap(t, resp)["id"].(string)
		return id
	}
	ownerID := register("owner@example.com")
	outsiderID := register("outsider@example.com")

	orgs, err := st.OrgsByOwner(context.Background(), ownerID)
	if err != nil || len(orgs) != 1 {
		t.Fatalf("orgs by owner = %+v, %v", orgs, err)
	}
	orgID := orgs[0].ID

	// A viewer token for the outsider: no org label, so the org/ownership
	// rungs cannot lift it above the token role.
	viewerSecret, _, err := Mint(context.Background(), st, MintRequest{Name: "viewer", Role: "viewer", AccountID: outsiderID})
	if err != nil {
		t.Fatalf("mint viewer: %v", err)
	}

	t.Run("missing bearer", func(t *testing.T) {
		code, resp := doJSON(t, h, "GET", "/api/v1/auth/token", "", nil)
		if code != http.StatusBadRequest || resp["success"] != false {
			t.Fatalf("= %d %v", code, resp)
		}
		if msg := errString(t, resp); !strings.Contains(msg, "missing bearer token") {
			t.Fatalf("error = %q", msg)
		}
	})

	t.Run("unknown bearer", func(t *testing.T) {
		code, resp := doJSON(t, h, "GET", "/api/v1/auth/token", "nope", nil)
		if code != http.StatusBadRequest || !strings.Contains(errString(t, resp), "unknown token") {
			t.Fatalf("= %d %v", code, resp)
		}
	})

	t.Run("viewer cannot issue for another account", func(t *testing.T) {
		code, resp := doJSON(t, h, "POST", "/api/v1/auth/token", viewerSecret, map[string]any{
			"account_id": ownerID, "name": "escalation", "role": "admin",
		})
		// Message updated when cross-account issuance was tightened to admin
		// (the behavior asserted here — refusal — is unchanged).
		if code != http.StatusBadRequest || !strings.Contains(errString(t, resp), "insufficient permission to issue a token for another account") {
			t.Fatalf("= %d %v", code, resp)
		}
	})

	t.Run("viewer may issue for itself", func(t *testing.T) {
		code, resp := doJSON(t, h, "POST", "/api/v1/auth/token", viewerSecret, map[string]any{
			"account_id": outsiderID, "name": "self",
		})
		if code != http.StatusOK || resp["success"] != true {
			t.Fatalf("= %d %v", code, resp)
		}
		// An unknown requested role degrades to viewer, never escalates.
		if role := dataMap(t, resp)["role"]; role != "viewer" {
			t.Fatalf("role = %v; want viewer", role)
		}
	})

	t.Run("org ownership lifts a viewer-labeled token", func(t *testing.T) {
		// Deliberate divergence from the Rust issue_token check, which compared
		// the TOKEN role against can_write. The port gates on the resolved role
		// (owner > org role > token role), and Rust's own doc comment for this
		// handler says the caller must be "an org admin or the account owner" —
		// which is exactly what the resolution implements. An org owner can
		// always re-mint a token for itself anyway, so the token-role check was
		// a speed bump, not a boundary.
		ownerViewer, _, err := Mint(context.Background(), st, MintRequest{
			Name: "owner-viewer", Role: "viewer", AccountID: ownerID, OrgID: orgID,
		})
		if err != nil {
			t.Fatalf("mint owner viewer: %v", err)
		}
		ctx, err := Resolve(st, reqWith(ownerViewer))
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if ctx.Via != ViaOwner || ctx.Role != Admin {
			t.Fatalf("ctx = %+v; want owner/Admin", ctx)
		}
		code, resp := doJSON(t, h, "POST", "/api/v1/auth/token", ownerViewer, map[string]any{
			"account_id": outsiderID, "name": "issued-by-owner",
		})
		if code != http.StatusOK || resp["success"] != true {
			t.Fatalf("= %d %v", code, resp)
		}
	})

	t.Run("member cannot add members, outsider cannot list", func(t *testing.T) {
		memberID := register("member@example.com")
		if _, err := AddOrgMember(context.Background(), st, orgID, memberID, OrgRoleMember); err != nil {
			t.Fatalf("add member: %v", err)
		}
		memberSecret, _, err := Mint(context.Background(), st, MintRequest{Name: "member", Role: "admin", AccountID: memberID, OrgID: orgID})
		if err != nil {
			t.Fatalf("mint member: %v", err)
		}
		code, resp := doJSON(t, h, "POST", "/api/v1/auth/org/"+orgID+"/member", memberSecret, map[string]string{
			"account_id": outsiderID, "role": "member",
		})
		if code != http.StatusBadRequest || !strings.Contains(errString(t, resp), "org admin+ required") {
			t.Fatalf("member add = %d %v", code, resp)
		}

		outsiderSecret, _, err := Mint(context.Background(), st, MintRequest{Name: "outsider", Role: "admin", AccountID: outsiderID})
		if err != nil {
			t.Fatalf("mint outsider: %v", err)
		}
		code, resp = doJSON(t, h, "GET", "/api/v1/auth/org/"+orgID+"/members", outsiderSecret, nil)
		if code != http.StatusBadRequest || !strings.Contains(errString(t, resp), "not an org member") {
			t.Fatalf("outsider list = %d %v", code, resp)
		}

		// Resource claims are account-scoped: no account label, no claim.
		code, resp = doJSON(t, h, "POST", "/api/v1/auth/resource/claim", "", map[string]any{
			"resource_type": "knowledge", "resource_id": "entry-9",
		})
		if code != http.StatusBadRequest {
			t.Fatalf("claim without bearer = %d %v", code, resp)
		}
	})

	t.Run("ttl_secs is range-checked and forwarded", func(t *testing.T) {
		// Beyond a time.Duration's whole-second range the conversion wraps to
		// 0, which Mint reads as "never expires" — must be rejected instead.
		code, resp := doJSON(t, h, "POST", "/api/v1/auth/token", viewerSecret, map[string]any{
			"account_id": outsiderID, "name": "forever", "ttl_secs": int64(1) << 62,
		})
		if code != http.StatusBadRequest || !strings.Contains(errString(t, resp), "ttl_secs out of range") {
			t.Fatalf("huge ttl = %d %v", code, resp)
		}

		// A negative TTL mints an already-expired token (Rust's ttl_secs +
		// now arithmetic), so it can never authenticate.
		code, resp = doJSON(t, h, "POST", "/api/v1/auth/token", viewerSecret, map[string]any{
			"account_id": outsiderID, "name": "expired", "ttl_secs": -10,
		})
		if code != http.StatusOK {
			t.Fatalf("negative ttl issue = %d %v", code, resp)
		}
		expired, _ := dataMap(t, resp)["access_token"].(string)
		if code, resp := doJSON(t, h, "GET", "/api/v1/auth/token", expired, nil); code != http.StatusBadRequest {
			t.Fatalf("expired token authenticated: %d %v", code, resp)
		}
	})

	t.Run("env token has no account for account-scoped endpoints", func(t *testing.T) {
		t.Setenv("LEANKG_TOKEN_ADMIN", "env-admin")
		code, resp := doJSON(t, h, "POST", "/api/v1/auth/org", "env-admin", "Env org")
		if code != http.StatusBadRequest || !strings.Contains(errString(t, resp), "account-scoped endpoint") {
			t.Fatalf("= %d %v", code, resp)
		}
	})

	t.Run("revoke is idempotent and takes effect", func(t *testing.T) {
		secret, tok, err := Mint(context.Background(), st, MintRequest{Name: "doomed", Role: "viewer", AccountID: ownerID})
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		code, resp := doJSON(t, h, "POST", "/api/v1/auth/token/revoke", viewerSecret, map[string]string{"token_id": "no-such-id"})
		if code != http.StatusOK || resp["data"] != false {
			t.Fatalf("unknown id revoke = %d %v", code, resp)
		}
		ownerBearer, _, err := Mint(context.Background(), st, MintRequest{Name: "owner-bearer", Role: "viewer", AccountID: ownerID})
		if err != nil {
			t.Fatalf("mint owner bearer: %v", err)
		}
		code, resp = doJSON(t, h, "POST", "/api/v1/auth/token/revoke", ownerBearer, map[string]string{"token_id": tok.ID})
		if code != http.StatusOK || resp["data"] != true {
			t.Fatalf("revoke = %d %v", code, resp)
		}
		code, resp = doJSON(t, h, "POST", "/api/v1/auth/token/revoke", ownerBearer, map[string]string{"token_id": tok.ID})
		if code != http.StatusOK || resp["data"] != false {
			t.Fatalf("second revoke = %d %v", code, resp)
		}
		if code, _ := doJSON(t, h, "GET", "/api/v1/auth/token", secret, nil); code == http.StatusOK {
			t.Fatal("a revoked token must not authenticate")
		}
	})

	t.Run("bad json body", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader("{"))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid JSON body") {
			t.Fatalf("= %d %s", rec.Code, rec.Body.String())
		}
	})

	// The outsider's own token stays scoped to itself.
	// The outsider never joined the org.
	_ = outsiderID
}

// TestTokenListNeverReturnsHash pins CX-03: the list JSON carries no secret
// key and no at-rest hash, for any caller.
func TestTokenListNeverReturnsHash(t *testing.T) {
	clearTokenEnv(t)
	st := openAuthStore(t)
	h := newAuthServer(t, st)

	const account = "acct-list"
	bearer, bearerRow, err := Mint(context.Background(), st, MintRequest{Name: "viewer", Role: "viewer", AccountID: account})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if _, _, err := Mint(context.Background(), st, MintRequest{Name: "sibling", Role: "viewer", AccountID: account}); err != nil {
		t.Fatalf("mint sibling: %v", err)
	}

	code, resp := doJSON(t, h, "GET", "/api/v1/auth/token", bearer, nil)
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("list = %d %v", code, resp)
	}
	rows, ok := resp["data"].([]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("list rows = %v; want 2", resp["data"])
	}
	for _, row := range rows {
		m, _ := row.(map[string]any)
		if _, has := m["secret"]; has {
			t.Fatalf("list row carries a secret key: %v", m)
		}
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), bearerRow.Secret) {
		t.Fatalf("list leaks the at-rest hash %s", bearerRow.Secret)
	}
	if strings.Contains(string(raw), bearer) {
		t.Fatal("list leaks the plaintext bearer")
	}
}

// TestTokenRevokeNeedsOwnerOrAdmin pins CX-03: revoking another account's
// token is refused with 403 unless the caller owns it or is an admin.
func TestTokenRevokeNeedsOwnerOrAdmin(t *testing.T) {
	clearTokenEnv(t)
	st := openAuthStore(t)
	h := newAuthServer(t, st)

	const owner, outsider, admin = "acct-owner", "acct-outsider", "acct-admin"
	mint := func(name, role, account string) (string, store.Token) {
		t.Helper()
		secret, row, err := Mint(context.Background(), st, MintRequest{Name: name, Role: role, AccountID: account})
		if err != nil {
			t.Fatalf("mint %s: %v", name, err)
		}
		return secret, row
	}
	revoke := func(bearer, id string) (int, map[string]any) {
		t.Helper()
		return doJSON(t, h, "POST", "/api/v1/auth/token/revoke", bearer, map[string]string{"token_id": id})
	}

	outsiderBearer, _ := mint("outsider", "viewer", outsider)
	ownerBearer, _ := mint("owner", "viewer", owner)
	adminBearer, _ := mint("admin", "admin", admin)

	t.Run("viewer cannot revoke another account's token", func(t *testing.T) {
		victimSecret, victim := mint("victim-a", "viewer", owner)
		code, resp := revoke(outsiderBearer, victim.ID)
		if code != http.StatusForbidden || resp["success"] != false {
			t.Fatalf("= %d %v; want 403", code, resp)
		}
		if code, _ := doJSON(t, h, "GET", "/api/v1/auth/token", victimSecret, nil); code != http.StatusOK {
			t.Fatalf("refused revoke must leave the victim token usable: %d", code)
		}
	})

	t.Run("owner can revoke own token", func(t *testing.T) {
		_, victim := mint("victim-b", "viewer", owner)
		code, resp := revoke(ownerBearer, victim.ID)
		if code != http.StatusOK || resp["data"] != true {
			t.Fatalf("= %d %v; want 200 true", code, resp)
		}
	})

	t.Run("admin can revoke any token", func(t *testing.T) {
		_, victim := mint("victim-c", "viewer", owner)
		code, resp := revoke(adminBearer, victim.ID)
		if code != http.StatusOK || resp["data"] != true {
			t.Fatalf("= %d %v; want 200 true", code, resp)
		}
	})

	t.Run("unknown id stays a no-op for any caller", func(t *testing.T) {
		code, resp := revoke(outsiderBearer, "no-such-id")
		if code != http.StatusOK || resp["data"] != false {
			t.Fatalf("= %d %v; want 200 false", code, resp)
		}
	})
}

// TestTokenListScopesProcessLevelCallers pins #586: an env token has no
// account, so it is a process-level caller. A process-level viewer must not see
// other accounts' token rows. Only a process-level admin sees the full list.
func TestTokenListScopesProcessLevelCallers(t *testing.T) {
	clearTokenEnv(t)
	st := openAuthStore(t)
	h := newAuthServer(t, st)
	for _, acct := range []string{"acct-scope-one", "acct-scope-two"} {
		if _, _, err := Mint(context.Background(), st, MintRequest{Name: acct, Role: "viewer", AccountID: acct}); err != nil {
			t.Fatalf("mint %s: %v", acct, err)
		}
	}
	t.Setenv("LEANKG_TOKEN_VIEWER", "env-viewer")
	t.Setenv("LEANKG_TOKEN_ADMIN", "env-admin")

	rowsFor := func(bearer string) []any {
		t.Helper()
		code, resp := doJSON(t, h, "GET", "/api/v1/auth/token", bearer, nil)
		if code != http.StatusOK {
			t.Fatalf("list as %s = %d %v; want 200", bearer, code, resp)
		}
		rows, ok := resp["data"].([]any)
		if !ok {
			t.Fatalf("list data = %v; want an array", resp["data"])
		}
		return rows
	}
	if rows := rowsFor("env-viewer"); len(rows) != 0 {
		t.Fatalf("process-level viewer listed %d token rows; want 0", len(rows))
	}
	if rows := rowsFor("env-admin"); len(rows) != 2 {
		t.Fatalf("process-level admin listed %d token rows; want 2", len(rows))
	}
}

// TestRegisterDuplicateEmailIsGeneric pins #586: registering an email that
// already exists must not reveal that it exists. The duplicate answers the same
// 400 envelope and generic text as the other registration failure, and never
// echoes the email back.
func TestRegisterDuplicateEmailIsGeneric(t *testing.T) {
	clearTokenEnv(t)
	st := openAuthStore(t)
	h := newAuthServer(t, st)
	const email = "taken@example.com"
	body := func(pw string) map[string]string {
		return map[string]string{"email": email, "password": pw, "name": "Taken"}
	}

	if code, resp := doJSON(t, h, "POST", "/api/v1/auth/register", "", body("password123")); code != http.StatusOK {
		t.Fatalf("first register = %d %v", code, resp)
	}
	code, dup := doJSON(t, h, "POST", "/api/v1/auth/register", "", body("password123"))
	if code != http.StatusBadRequest || dup["success"] != false {
		t.Fatalf("duplicate register = %d %v; want 400 failure", code, dup)
	}
	if got, _ := dup["error"].(string); got != "registration failed" {
		t.Fatalf("duplicate error = %q; want generic %q", got, "registration failed")
	}
	raw, _ := json.Marshal(dup)
	if strings.Contains(strings.ToLower(string(raw)), "taken@example.com") {
		t.Fatalf("duplicate response echoes the email: %s", raw)
	}

	code, other := doJSON(t, h, "POST", "/api/v1/auth/register", "", body("short"))
	if code != http.StatusBadRequest || other["success"] != false {
		t.Fatalf("short-password register = %d %v; want 400 failure", code, other)
	}
}

// TestAuthBodyOverLimitIs413 pins #601 item 5: an unauthenticated auth body over
// the 1 MiB cap answers 413 in the envelope with the catalog code, on every
// route that decodes a body.
func TestAuthBodyOverLimitIs413(t *testing.T) {
	clearTokenEnv(t)
	h := newAuthServer(t, openAuthStore(t))
	const limit = 1 << 20
	cases := []struct{ path, prefix, suffix string }{
		{"/api/v1/auth/register", `{"email":"big@example.com","password":"`, `"}`},
		{"/api/v1/auth/org", `"`, `"`},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			raw := tc.prefix + strings.Repeat("p", limit+1-len(tc.prefix)-len(tc.suffix)) + tc.suffix
			req := httptest.NewRequest("POST", tc.path, strings.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			var resp map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("response is not JSON (%d): %s", rec.Code, rec.Body.String())
			}
			if rec.Code != http.StatusRequestEntityTooLarge || resp["success"] != false {
				t.Fatalf("= %d %v; want 413 failure envelope", rec.Code, resp)
			}
			if resp["code"] != errs.RequestTooLarge.Code {
				t.Fatalf("code = %v, want %s", resp["code"], errs.RequestTooLarge.Code)
			}
		})
	}
}

// upsertDownStore fails every account write with driver text, the way a broken
// database does.
type upsertDownStore struct{ Store }

func (upsertDownStore) AccountUpsert(context.Context, store.Account) error {
	return errors.New(`pq: password authentication failed for user "leankg_svc" dsn=postgres://leankg_svc:hunter2@db.internal:5432/leankg`)
}

// TestRegisterStoreFailureIsGeneric pins #601 item 6: a store failure answers
// 500 with a generic message, and the driver text never reaches the client.
func TestRegisterStoreFailureIsGeneric(t *testing.T) {
	clearTokenEnv(t)
	h := Routes(upsertDownStore{openAuthStore(t)})
	code, resp := doJSON(t, h, "POST", "/api/v1/auth/register", "", map[string]string{
		"email": "down@example.com", "password": "password123", "name": "Down",
	})
	if code != http.StatusInternalServerError || resp["success"] != false {
		t.Fatalf("= %d %v; want 500 failure envelope", code, resp)
	}
	if msg := errString(t, resp); msg != "internal error" {
		t.Fatalf("error = %q; want generic %q", msg, "internal error")
	}
	raw, _ := json.Marshal(resp)
	for _, leak := range []string{"password authentication", "hunter2", "dsn="} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("response leaks driver text %q: %s", leak, raw)
		}
	}
}

// tokenListDownStore fails the token listing, the way a broken database does.
type tokenListDownStore struct{ Store }

func (tokenListDownStore) TokenList(context.Context) ([]store.Token, error) {
	return nil, errors.New("database disk image is malformed: /var/lib/leankg/db.sqlite")
}

// TestAuthRoutesStoreFailureIsGeneric pins the #601 driver-text rule on the
// token routes: a store failure answers 500 with the generic text and never the
// driver message. An env admin bearer resolves without a store call, so the
// route's own TokenList is the call that fails.
func TestAuthRoutesStoreFailureIsGeneric(t *testing.T) {
	clearTokenEnv(t)
	t.Setenv("LEANKG_TOKEN_ADMIN", "env-admin-secret")
	h := Routes(tokenListDownStore{openAuthStore(t)})
	cases := []struct {
		name, method, path string
		body               any
	}{
		{"token list", "GET", "/api/v1/auth/token", nil},
		{"token revoke", "POST", "/api/v1/auth/token/revoke", map[string]string{"token_id": "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, resp := doJSON(t, h, tc.method, tc.path, "env-admin-secret", tc.body)
			if code != http.StatusInternalServerError || resp["success"] != false {
				t.Fatalf("= %d %v; want 500 failure envelope", code, resp)
			}
			if msg := errString(t, resp); msg != "internal error" {
				t.Fatalf("error = %q; want generic %q", msg, "internal error")
			}
			raw, _ := json.Marshal(resp)
			for _, leak := range []string{"disk image", "/var/lib", "malformed"} {
				if strings.Contains(string(raw), leak) {
					t.Fatalf("response leaks driver text %q: %s", leak, raw)
				}
			}
		})
	}
}

// TestAuthRoutesValidationMessageUnchanged pins that a client error keeps its
// message and status: a bearer that does not authenticate is still 400
// "auth: unknown token", even on a store that is broken.
func TestAuthRoutesValidationMessageUnchanged(t *testing.T) {
	clearTokenEnv(t)
	t.Setenv("LEANKG_TOKEN_ADMIN", "env-admin-secret")
	h := Routes(tokenListDownStore{openAuthStore(t)})
	code, resp := doJSON(t, h, "GET", "/api/v1/auth/token", "not-a-real-token", nil)
	if code != http.StatusBadRequest || errString(t, resp) != "auth: unknown token" {
		t.Fatalf("= %d %v; want 400 %q", code, resp, "auth: unknown token")
	}
}

// TestClaimWithoutRequiredFieldIsClientError pins that a missing claim field is
// a 400 with its message. The required check runs before the store, so the
// store's own required-field error is not classified as a store fault.
func TestClaimWithoutRequiredFieldIsClientError(t *testing.T) {
	clearTokenEnv(t)
	st := openAuthStore(t)
	acct, err := Register(context.Background(), st, "claimer@example.com", "password123", "C")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	secret, _, err := Mint(context.Background(), st, MintRequest{Name: "c", Role: "admin", AccountID: acct.ID})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	code, resp := doJSON(t, newAuthServer(t, st), "POST", "/api/v1/auth/resource/claim", secret, map[string]string{
		"resource_type": "", "resource_id": "r1",
	})
	const want = "resource claim: resource_type, resource_id and owner_account_id are required"
	if code != http.StatusBadRequest || errString(t, resp) != want {
		t.Fatalf("= %d %v; want 400 %q", code, resp, want)
	}
}

// TestTokenIssueNeedsWriteForProcessCaller pins #601 B2: an env token has no
// account, so it cannot own a minted token. An env viewer must not persist a DB
// token through the public issuance route; an env contributor or admin still can.
func TestTokenIssueNeedsWriteForProcessCaller(t *testing.T) {
	st := openAuthStore(t)
	h := newAuthServer(t, st)
	const secret = "env-secret-for-issuance"
	cases := []struct {
		name, env string
		want      int
	}{
		{"viewer refused", "LEANKG_TOKEN_VIEWER", http.StatusForbidden},
		{"contributor allowed", "LEANKG_TOKEN_CONTRIBUTOR", http.StatusOK},
		{"admin allowed", "LEANKG_TOKEN_ADMIN", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearTokenEnv(t)
			t.Setenv(tc.env, secret)
			before, err := st.TokenList(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			code, resp := doJSON(t, h, "POST", "/api/v1/auth/token", secret, map[string]any{
				"name": "persist", "role": "viewer",
			})
			after, err := st.TokenList(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if code != tc.want {
				t.Fatalf("status = %d %v, want %d", code, resp, tc.want)
			}
			if tc.want == http.StatusForbidden {
				if resp["success"] != false || !strings.Contains(errString(t, resp), "write access required") {
					t.Fatalf("refusal = %v, want a failure envelope naming write access", resp)
				}
				if len(after) != len(before) {
					t.Fatalf("token rows %d -> %d; a refused issuance must mint nothing", len(before), len(after))
				}
			} else if len(after) != len(before)+1 {
				t.Fatalf("token rows %d -> %d, want one minted", len(before), len(after))
			}
		})
	}
}
