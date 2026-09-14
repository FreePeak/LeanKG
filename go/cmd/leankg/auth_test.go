package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/auth"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// authField pulls a "  Label: value" field out of the create output.
func authField(t *testing.T, out, label string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), label) {
			return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), label))
		}
	}
	t.Fatalf("output has no %q field:\n%s", label, out)
	return ""
}

// TestAuthTokenCLI covers the token half of Rust's `auth` command end to end:
// create shows the plaintext once, list reports state, revoke soft-revokes,
// and the revoked secret stops authenticating.
func TestAuthTokenCLI(t *testing.T) {
	dir := t.TempDir() // fresh project: create must migrate the schema itself

	stdout, stderr, code := runCLI(t, "auth", "token", "create", "--project", dir,
		"--name", "ci", "--role", "contributor")
	if code != 0 {
		t.Fatalf("auth token create exit = %d, stderr: %s", code, stderr)
	}
	id := authField(t, stdout, "ID:")
	role := authField(t, stdout, "Role:")
	if role != "contributor" {
		t.Fatalf("create role = %q", role)
	}
	if !strings.Contains(stdout, "IMPORTANT: Save this token - it will not be shown again:") {
		t.Fatalf("create output must warn about the one-time secret:\n%s", stdout)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	secret := strings.TrimSpace(lines[len(lines)-1])
	if !strings.HasPrefix(secret, "lkg_") {
		t.Fatalf("secret = %q, want an lkg_ token", secret)
	}

	dbPath := filepath.Join(dir, ".leankg", "leankg.db")
	verify := func() (auth.Grant, bool, error) {
		t.Helper()
		st, err := store.Open(dbPath, store.RO)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		return auth.Verify(st, secret)
	}
	grant, ok, err := verify()
	if err != nil || !ok || grant.TokenID != id || grant.Role != auth.Contributor {
		t.Fatalf("minted token must authenticate: ok=%v grant=%+v err=%v", ok, grant, err)
	}

	stdout, _, code = runCLI(t, "auth", "token", "list", "--project", dir)
	if code != 0 {
		t.Fatalf("auth token list exit = %d", code)
	}
	for _, want := range []string{id, "Name: ci", "Role: contributor", "revoked: false"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("list output missing %q:\n%s", want, stdout)
		}
	}

	stdout, stderr, code = runCLI(t, "auth", "token", "revoke", "--project", dir, "--token-id", id)
	if code != 0 {
		t.Fatalf("auth token revoke exit = %d, stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "revoked.") {
		t.Fatalf("revoke output = %q", stdout)
	}
	if _, ok, err := verify(); ok || !errors.Is(err, store.ErrTokenRevoked) {
		t.Fatalf("revoked token must be rejected: ok=%v err=%v", ok, err)
	}

	// Revocation is idempotent and unknown ids are reported, not fatal.
	stdout, _, code = runCLI(t, "auth", "token", "revoke", "--project", dir, "--token-id", id)
	if code != 0 || !strings.Contains(stdout, "already revoked") {
		t.Fatalf("second revoke = %q (code %d)", stdout, code)
	}
	stdout, _, code = runCLI(t, "auth", "token", "revoke", "--project", dir, "--token-id", "nope")
	if code != 0 || !strings.Contains(stdout, "not found") {
		t.Fatalf("unknown-id revoke = %q (code %d)", stdout, code)
	}
	stdout, _, code = runCLI(t, "auth", "token", "list", "--project", dir)
	if code != 0 || !strings.Contains(stdout, "revoked: true") {
		t.Fatalf("list after revoke = %q", stdout)
	}
}

// TestAuthTokenCLIValidation pins the flag and role guards.
func TestAuthTokenCLIValidation(t *testing.T) {
	dir := t.TempDir()

	if _, stderr, code := runCLI(t, "auth", "token", "create", "--project", dir); code != 2 ||
		!strings.Contains(stderr, "--name is required") {
		t.Fatalf("missing --name: code=%d stderr=%s", code, stderr)
	}
	_, stderr, code := runCLI(t, "auth", "token", "create", "--project", dir, "--name", "x", "--role", "root")
	if code != 1 || !strings.Contains(stderr, "unknown role") {
		t.Fatalf("bad role: code=%d stderr=%s", code, stderr)
	}
	if _, stderr, code := runCLI(t, "auth", "token", "create", "--project", dir, "--name", "x", "--ttl", "soon"); code != 2 ||
		!strings.Contains(stderr, "invalid --ttl") {
		t.Fatalf("bad ttl: code=%d stderr=%s", code, stderr)
	}
	if _, stderr, code := runCLI(t, "auth", "token", "revoke", "--project", dir); code != 2 ||
		!strings.Contains(stderr, "--token-id is required") {
		t.Fatalf("missing --token-id: code=%d stderr=%s", code, stderr)
	}
	if _, stderr, code := runCLI(t, "auth", "register"); code != 2 || !strings.Contains(stderr, "--email is required") {
		t.Fatalf("register without flags: code=%d stderr=%s", code, stderr)
	}

	// A TTL-scoped token still mints and lists.
	if stdout, stderr, code := runCLI(t, "auth", "token", "create", "--project", dir,
		"--name", "short", "--ttl", "1h", "--scopes", "read, write"); code != 0 {
		t.Fatalf("ttl/scopes create: code=%d stderr=%s", code, stderr)
	} else if !strings.Contains(stdout, "Access token issued:") {
		t.Fatalf("ttl/scopes create output = %q", stdout)
	}
}

// TestAuthRegisterCLI covers the account half of Rust's auth command now that
// internal/auth carries the accounts/orgs subsystem: register prints the
// account, and tokens can be labelled with its id and listed per account.
func TestAuthRegisterCLI(t *testing.T) {
	dir := t.TempDir()

	stdout, stderr, code := runCLI(t, "auth", "register", "--project", dir,
		"--email", "Dev@Example.com", "--password", "correct horse", "--name", "Dev")
	if code != 0 {
		t.Fatalf("auth register exit = %d, stderr: %s", code, stderr)
	}
	accountID := authField(t, stdout, "ID:")
	// auth.Register normalizes the email (lowercase) before persisting.
	for _, want := range []string{"Account registered:", "dev@example.com", "Name:  Dev"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("register output missing %q:\n%s", want, stdout)
		}
	}

	// Duplicate email and short passwords are rejected by the store layer.
	_, stderr, code = runCLI(t, "auth", "register", "--project", dir,
		"--email", "dev@example.com", "--password", "correct horse", "--name", "Dev")
	if code != 1 || !strings.Contains(stderr, "already exists") {
		t.Fatalf("duplicate email: code=%d stderr=%s", code, stderr)
	}
	_, stderr, code = runCLI(t, "auth", "register", "--project", dir,
		"--email", "other@example.com", "--password", "short", "--name", "Other")
	if code != 1 || !strings.Contains(stderr, "at least 8 characters") {
		t.Fatalf("short password: code=%d stderr=%s", code, stderr)
	}

	// A token labelled with the account id is visible in that account's list.
	if stdout, stderr, code = runCLI(t, "auth", "token", "create", "--project", dir,
		"--name", "for-dev", "--role", "viewer", "--account-id", accountID); code != 0 {
		t.Fatalf("token create with account: code=%d stderr=%s", code, stderr)
	}
	stdout, stderr, code = runCLI(t, "auth", "token", "list", "--project", dir, "--account-id", accountID)
	if code != 0 || !strings.Contains(stdout, "Access tokens for "+accountID+":") || !strings.Contains(stdout, "Name: for-dev") {
		t.Fatalf("account-scoped list: code=%d stderr=%s\n%s", code, stderr, stdout)
	}
	stdout, _, code = runCLI(t, "auth", "token", "list", "--project", dir, "--account-id", "someone-else")
	if code != 0 || !strings.Contains(stdout, "No access tokens for account someone-else.") {
		t.Fatalf("empty account list = %q", stdout)
	}
	stdout, _, code = runCLI(t, "auth", "token", "list", "--project", dir)
	if code != 0 || !strings.Contains(stdout, "Access tokens:") {
		t.Fatalf("unscoped list = %q", stdout)
	}
}
