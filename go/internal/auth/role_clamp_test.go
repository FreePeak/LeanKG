package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// TestViewerCannotMintAboveItsRole pins the privilege ceiling: a viewer bearer
// issues a token for its own account with role=admin (the handler only guarded
// CROSS-account issuance), which would be a straight escalation. Rust shipped
// that hole while its doc comment claimed admin-only issuance.
func TestViewerCannotMintAboveItsRole(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}

	acct, err := Register(st, "viewer@example.com", "password123", "V")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	// A viewer token bound to that account.
	viewerSecret, _, err := Mint(st, MintRequest{Name: "v", Role: "viewer", AccountID: acct.ID})
	if err != nil {
		t.Fatalf("mint viewer: %v", err)
	}

	body := `{"account_id":"` + acct.ID + `","role":"admin","name":"esc"}`
	req := httptest.NewRequest("POST", "/api/v1/auth/token", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+viewerSecret)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	Routes(st).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Success bool `json:"success"`
		Data    struct {
			AccessToken string `json:"access_token"`
			Role        string `json:"role"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	if !env.Success {
		t.Fatalf("issuance refused outright: %s", rec.Body.String())
	}
	if env.Data.Role != "viewer" {
		t.Fatalf("issued role = %q, want viewer (no escalation)", env.Data.Role)
	}
	g, ok, err := Verify(st, env.Data.AccessToken)
	if err != nil || !ok {
		t.Fatalf("verify issued token: ok=%v err=%v", ok, err)
	}
	if g.Role != Viewer {
		t.Fatalf("resolved role = %v, want Viewer", g.Role)
	}
}
