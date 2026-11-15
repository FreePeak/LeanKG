package mcp

import (
	"context"
	"errors"
	"time"

	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/errs"
	"github.com/FreePeak/LeanKG/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// authenticateTokenTTL bounds a login session: the bearer token it mints
// expires this long after the login, so a session is never eternal.
const authenticateTokenTTL = 24 * time.Hour

// authenticateSchema is the login tool's input. It is server-level, so it
// carries no `project` property and is never subject to project routing.
const authenticateSchema = `{
	"type": "object",
	"properties": {
		"username": {"type": "string", "minLength": 1, "description": "site-admin username (LEANKG_ADMIN_USERNAME in the server's .env, as written by leankg auth admin init)"},
		"password": {"type": "string", "minLength": 1, "description": "site-admin password (LEANKG_ADMIN_PASSWORD in the server's .env)"}
	},
	"required": ["username", "password"],
	"additionalProperties": false
}`

type authenticateArgs struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type authenticateResult struct {
	Token     string `json:"token"`
	Role      string `json:"role"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// handleAuthenticate verifies the site-admin login and returns a revocable
// bearer token of role admin. Every rejection is the generic invalid-credentials
// error, except a lockout, which names the rate limit. The password is never
// echoed, logged or recorded: capture drops this tool's arguments and result
// (see secretCall).
func (s *Server) handleAuthenticate(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var in authenticateArgs
	if err := unmarshalArgs(req, &in); err != nil {
		return nil, err
	}
	st := s.adminStore()
	if st == nil {
		return nil, errors.New("authenticate: no auth store is configured for this server")
	}
	account, err := auth.VerifyAdminLogin(ctx, st, in.Username, in.Password)
	switch {
	case errors.Is(err, auth.ErrAdminLoginRateLimited):
		return nil, errs.NewError(errs.Unauthorized,
			"too many failed sign-in attempts for this username; wait a minute before calling authenticate again", "")
	case errors.Is(err, auth.ErrInvalidAdminLogin):
		return nil, errs.NewError(errs.Unauthorized, "invalid credentials", "")
	case err != nil:
		return nil, err
	}
	token, row, err := auth.Mint(ctx, st, auth.MintRequest{
		Name:      "authenticate",
		Role:      auth.Admin.String(),
		AccountID: account.ID,
		TTL:       authenticateTokenTTL,
	})
	if err != nil {
		return nil, err
	}
	return textResult(authenticateResult{
		Token:     token,
		Role:      auth.Admin.String(),
		ExpiresAt: time.Unix(row.ExpiresAt, 0).UTC().Format(time.RFC3339),
	})
}

// adminStore is the store logins are checked against and tokens are minted
// into. serve hands it in with SetAuthStore for HTTP and stdio alike; nil means
// authenticate refuses rather than reaching into the engine.
func (s *Server) adminStore() store.Backend { return s.authStore }
