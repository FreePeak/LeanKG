package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/store"
)

// postJSON posts body to target with an optional bearer and returns the status
// and body. It is the POST counterpart of get.
func postJSON(h http.Handler, bearer, target, body string) (int, string) {
	req := httptest.NewRequest("POST", target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// mintRow issues a DB token and returns its plaintext secret and stored row, so
// a test can name the token id as well as use the bearer.
func (r grantRig) mintRow(role string, accountID string, scopes ...string) (string, store.Token) {
	r.t.Helper()
	secret, row, err := auth.Mint(context.Background(), r.st, auth.MintRequest{Name: r.t.Name(), Role: role, AccountID: accountID, Scopes: scopes})
	if err != nil {
		r.t.Fatal(err)
	}
	return secret, row
}

// TestProjectGrantTokenMint pins that minting over HTTP cannot widen a grant. A
// token minted by a project-restricted caller inherits the caller's project
// scopes: it reaches the caller's project and no other, and a body that asks for
// another project gets none of it. An unrestricted caller still mints an
// unrestricted token.
func TestProjectGrantTokenMint(t *testing.T) {
	r := newGrantRig(t)
	authH := http.NewServeMux()
	authH.Handle("/api/v1/auth/", auth.Routes(r.st))
	rest := r.restHandler()
	alphaTok := r.mint("contributor", "project:alpha")

	mint := func(bearer, body string) string {
		t.Helper()
		code, resp := postJSON(authH, bearer, "/api/v1/auth/token", body)
		if code != http.StatusOK {
			t.Fatalf("mint %s = %d; got %s", body, code, resp)
		}
		var wire struct {
			Data struct {
				AccessToken string `json:"access_token"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(resp), &wire); err != nil {
			t.Fatal(err)
		}
		return wire.Data.AccessToken
	}

	child := mint(alphaTok, `{"role":"contributor"}`)
	if code, body := get(t, rest, child, "/api/v1/status?project=beta"); code != http.StatusForbidden {
		t.Fatalf("token minted by an alpha-restricted caller reached beta = %d, want 403; got %s", code, body)
	}
	if code, body := get(t, rest, child, "/api/v1/status?project=alpha"); code != http.StatusOK {
		t.Fatalf("token minted by an alpha-restricted caller must reach alpha = %d; got %s", code, body)
	}
	if body := callTool(r.mcpHandler(), child, "status", `{"project":"beta"}`); !strings.Contains(body, "LEANKG_ERROR_PERMISSION_DENIED") {
		t.Fatalf("MCP beta through a minted child must be refused; got %s", body)
	}

	widened := mint(alphaTok, `{"role":"contributor","scopes":["project:beta"]}`)
	if code, body := get(t, rest, widened, "/api/v1/status?project=beta"); code != http.StatusForbidden {
		t.Fatalf("body-requested project:beta must be ignored; beta = %d, want 403; got %s", code, body)
	}

	unrestricted := mint(r.mint("admin"), `{"role":"viewer"}`)
	if code, body := get(t, rest, unrestricted, "/api/v1/status?project=beta"); code != http.StatusOK {
		t.Fatalf("token minted by an unrestricted caller must reach beta = %d; got %s", code, body)
	}
}

// TestProjectGrantTokenListRestricted pins that a project-restricted caller sees
// only its own token in the list. Another token's id, scopes and labels stay
// hidden, the same as for an unrestricted caller of another account.
func TestProjectGrantTokenListRestricted(t *testing.T) {
	r := newGrantRig(t)
	authH := http.NewServeMux()
	authH.Handle("/api/v1/auth/", auth.Routes(r.st))
	own, ownRow := r.mintRow("contributor", "", "project:alpha")
	_, otherRow := r.mintRow("admin", "")

	code, body := get(t, authH, own, "/api/v1/auth/token")
	if code != http.StatusOK {
		t.Fatalf("restricted token list = %d; got %s", code, body)
	}
	if strings.Contains(body, otherRow.ID) {
		t.Fatalf("restricted list reveals another token %s: %s", otherRow.ID, body)
	}
	if !strings.Contains(body, ownRow.ID) {
		t.Fatalf("restricted list must show its own token %s: %s", ownRow.ID, body)
	}
}

// TestProjectGrantTokenRevokeRestricted pins that a project-restricted admin
// cannot revoke another token. The answer is the same one an unknown id gets,
// and the other token keeps working.
func TestProjectGrantTokenRevokeRestricted(t *testing.T) {
	r := newGrantRig(t)
	authH := http.NewServeMux()
	authH.Handle("/api/v1/auth/", auth.Routes(r.st))
	restrictedAdmin := r.mint("admin", "project:alpha")
	otherSecret, otherRow := r.mintRow("admin", "")

	code, body := postJSON(authH, restrictedAdmin, "/api/v1/auth/token/revoke", `{"token_id":"`+otherRow.ID+`"}`)
	if code != http.StatusOK || !strings.Contains(body, `"data":false`) {
		t.Fatalf("restricted admin revoke of another token = %d %s; want 200 with data false", code, body)
	}
	if code, body := get(t, r.restHandler(), otherSecret, "/api/v1/status?project=beta"); code != http.StatusOK {
		t.Fatalf("the other token must still work after the refused revoke = %d; got %s", code, body)
	}
}

// TestProjectGrantAccountEndpointsRefused pins that account and org management
// is refused to a project-restricted caller: creating an org, adding a member
// and listing members all change or read other accounts' standing.
func TestProjectGrantAccountEndpointsRefused(t *testing.T) {
	r := newGrantRig(t)
	authH := http.NewServeMux()
	authH.Handle("/api/v1/auth/", auth.Routes(r.st))
	restricted, _ := r.mintRow("admin", "acct-a", "project:alpha")

	if code, body := postJSON(authH, restricted, "/api/v1/auth/org", `"sneaky"`); code != http.StatusForbidden {
		t.Fatalf("restricted org create = %d, want 403; got %s", code, body)
	}
	if code, body := postJSON(authH, restricted, "/api/v1/auth/org/any/member", `{"account_id":"acct-b","role":"admin"}`); code != http.StatusForbidden {
		t.Fatalf("restricted member add = %d, want 403; got %s", code, body)
	}
	if code, body := get(t, authH, restricted, "/api/v1/auth/org/any/members"); code != http.StatusForbidden {
		t.Fatalf("restricted member list = %d, want 403; got %s", code, body)
	}
}
