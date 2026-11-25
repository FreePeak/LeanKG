package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/errs"
	"github.com/FreePeak/LeanKG/internal/httpx"
)

// TestRefusedNamesTheTokenWhenABearerWasSent: a request that carried a bearer
// token was refused for that token, so the 401 must not send the caller to the
// login step. The code and status are the same as before.
func TestRefusedNamesTheTokenWhenABearerWasSent(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	r.Header.Set("Authorization", "Bearer not-a-real-token")
	err := Refused(r)
	if got := err.Code(); got != errs.Unauthorized.Code {
		t.Fatalf("code = %s, want %s", got, errs.Unauthorized.Code)
	}
	if err.Fix != errs.UnauthorizedBadTokenFix {
		t.Fatalf("fix = %q, want the bad-token hint %q", err.Fix, errs.UnauthorizedBadTokenFix)
	}
	if status := httpx.StatusFor(err); status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", status)
	}
	if !strings.Contains(err.Error(), "unknown, revoked or expired") {
		t.Fatalf("rendered error does not name the token: %q", err.Error())
	}
}

// TestRefusedKeepsTheLoginHintWithNoBearer: a request with no bearer token
// keeps the catalog's login hint, as does a non-bearer scheme and an empty
// bearer.
func TestRefusedKeepsTheLoginHintWithNoBearer(t *testing.T) {
	for _, header := range []string{"", "Basic dXNlcjpwYXNz", "Bearer "} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		if got := Refused(r).Fix; got != errs.Unauthorized.Fix {
			t.Fatalf("Authorization %q: fix = %q, want the login hint", header, got)
		}
	}
}

// TestGateAnswersABadBearerWithTheTokenHint is the REST path end to end: the
// gate refuses an unknown bearer with the token hint, and a request with no
// bearer with the login hint. Both answer 401 with the same stable code.
func TestGateAnswersABadBearerWithTheTokenHint(t *testing.T) {
	t.Setenv("LEANKG_TOKEN_ADMIN", "configured-admin-token-for-test")
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := MiddlewareWithStore(nil, ok)

	bad := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	bad.Header.Set("Authorization", "Bearer revoked-or-garbage")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, bad)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad bearer: status = %d, want 401", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "unknown, revoked or expired") || strings.Contains(body, "authenticate tool") {
		t.Fatalf("bad bearer: body must name the token, not the login step: %s", body)
	}
	if !strings.Contains(body, `"code":"LEANKG_ERROR_UNAUTHORIZED"`) {
		t.Fatalf("bad bearer: stable code missing: %s", body)
	}

	anon := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, anon)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "call the authenticate tool") {
		t.Fatalf("no bearer: want 401 with the login hint, got %d %s", rec.Code, rec.Body.String())
	}
}
