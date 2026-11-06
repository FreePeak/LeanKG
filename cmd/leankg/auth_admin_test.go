package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/envfile"
	"github.com/FreePeak/LeanKG/internal/store"
)

// Every bootstrap test uses its own username: the admin login throttle is
// process-wide, so shared usernames would carry failures between tests.

func adminInitStore(t *testing.T) store.Backend {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

func adminInitWriteEnv(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write env: %v", err)
	}
}

func adminInitText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestAdminInitGeneratesPasswordWhenAbsent(t *testing.T) {
	st := adminInitStore(t)
	envPath := filepath.Join(t.TempDir(), ".env")
	adminInitWriteEnv(t, envPath, "OTHER=1\n")

	outcome, err := bootstrapAdmin(st, "admin-gen", envPath)
	if err != nil || outcome != adminCreatedGenerated {
		t.Fatalf("bootstrap = %v, %v; want generated", outcome, err)
	}
	env, err := envfile.Load(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if env[adminUsernameKey] != "admin-gen" {
		t.Fatalf("env username = %q", env[adminUsernameKey])
	}
	password := env[adminPasswordKey]
	if len(password) != 43 {
		t.Fatalf("generated password length = %d, want 43", len(password))
	}
	if got := adminInitText(t, envPath); !strings.HasPrefix(got, "OTHER=1\n") {
		t.Fatalf("existing env lines were not preserved: %q", got)
	}
	if info, _ := os.Stat(envPath); info.Mode().Perm() != 0o600 {
		t.Fatalf("env mode = %o, want 600", info.Mode().Perm())
	}
	if _, err := auth.VerifyAdminLogin(st, "admin-gen", password); err != nil {
		t.Fatalf("written credentials do not verify: %v", err)
	}
}

func TestAdminInitCreatesFromEnvPassword(t *testing.T) {
	st := adminInitStore(t)
	envPath := filepath.Join(t.TempDir(), ".env")
	orig := "LEANKG_ADMIN_PASSWORD='env-provided-pw'\n"
	adminInitWriteEnv(t, envPath, orig)

	outcome, err := bootstrapAdmin(st, "admin-env", envPath)
	if err != nil || outcome != adminCreatedFromEnv {
		t.Fatalf("bootstrap = %v, %v; want created from env", outcome, err)
	}
	if _, err := auth.VerifyAdminLogin(st, "admin-env", "env-provided-pw"); err != nil {
		t.Fatalf("account must use the env password: %v", err)
	}
	if got, want := adminInitText(t, envPath), orig+"LEANKG_ADMIN_USERNAME=admin-env\n"; got != want {
		t.Fatalf("env = %q, want %q (password must not be regenerated)", got, want)
	}
}

func TestAdminInitNoopWhenCredentialsVerify(t *testing.T) {
	st := adminInitStore(t)
	if _, err := auth.CreateSiteAdmin(st, "admin-same", "same-password-1"); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(t.TempDir(), ".env")
	orig := "LEANKG_ADMIN_USERNAME=admin-same\nLEANKG_ADMIN_PASSWORD=same-password-1\n"
	adminInitWriteEnv(t, envPath, orig)
	if err := os.Chmod(envPath, 0o600); err != nil {
		t.Fatal(err)
	}
	before, _, _ := st.AccountByEmail("admin-same")

	outcome, err := bootstrapAdmin(st, "admin-same", envPath)
	if err != nil || outcome != adminAlreadyInitialised {
		t.Fatalf("bootstrap = %v, %v; want already initialised", outcome, err)
	}
	if got := adminInitText(t, envPath); got != orig {
		t.Fatalf("no-op changed the env file: %q", got)
	}
	after, _, _ := st.AccountByEmail("admin-same")
	if after.PasswordHash != before.PasswordHash {
		t.Fatal("no-op changed the stored credential")
	}
}

// An existing account whose credentials cannot be confirmed is an error, and
// nothing is changed: the plaintext cannot be recovered from the hash.
// A no-op run on a world-readable env file tightens it to 0600, because the
// file holds the plaintext password.
func TestAdminInitNoopTightensEnvModeTo0600(t *testing.T) {
	st := adminInitStore(t)
	if _, err := auth.CreateSiteAdmin(st, "admin-mode", "mode-password-1"); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(t.TempDir(), ".env")
	orig := "LEANKG_ADMIN_USERNAME=admin-mode\nLEANKG_ADMIN_PASSWORD=mode-password-1\n"
	adminInitWriteEnv(t, envPath, orig)
	if err := os.Chmod(envPath, 0o644); err != nil {
		t.Fatal(err)
	}

	outcome, err := bootstrapAdmin(st, "admin-mode", envPath)
	if err != nil || outcome != adminAlreadyInitialisedTightened {
		t.Fatalf("bootstrap = %v, %v; want already initialised with mode tightened", outcome, err)
	}
	if info, _ := os.Stat(envPath); info.Mode().Perm() != 0o600 {
		t.Fatalf("env mode = %o, want 600", info.Mode().Perm())
	}
	if got := adminInitText(t, envPath); got != orig {
		t.Fatalf("no-op changed the env content: %q", got)
	}
}

func TestAdminInitFailsWhenExistingAccountCannotBeConfirmed(t *testing.T) {
	cases := []struct {
		name, username, env string
		plain               bool
	}{
		{name: "env lacks password", username: "admin-nopw", env: "OTHER=1\n"},
		{name: "wrong password", username: "admin-wrongpw", env: "LEANKG_ADMIN_PASSWORD=not-the-password\n"},
		{name: "not a site admin", username: "ops-plain@example.com", env: "LEANKG_ADMIN_PASSWORD=plain-password-1\n", plain: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := adminInitStore(t)
			if c.plain {
				if _, err := auth.Register(st, c.username, "plain-password-1", "Ops"); err != nil {
					t.Fatal(err)
				}
			} else if _, err := auth.CreateSiteAdmin(st, c.username, "real-password-1"); err != nil {
				t.Fatal(err)
			}
			envPath := filepath.Join(t.TempDir(), ".env")
			adminInitWriteEnv(t, envPath, c.env)
			before, _, _ := st.AccountByEmail(auth.NormalizeEmail(c.username))

			if _, err := bootstrapAdmin(st, c.username, envPath); err == nil {
				t.Fatal("bootstrap must fail when the existing account cannot be confirmed")
			}
			if got := adminInitText(t, envPath); got != c.env {
				t.Fatalf("failed bootstrap changed the env file: %q", got)
			}
			after, _, _ := st.AccountByEmail(auth.NormalizeEmail(c.username))
			if after.PasswordHash != before.PasswordHash || after.SiteAdmin != before.SiteAdmin {
				t.Fatal("failed bootstrap changed the account")
			}
		})
	}
}

func TestAdminInitRefusesSecondSiteAdmin(t *testing.T) {
	st := adminInitStore(t)
	if _, err := auth.CreateSiteAdmin(st, "root-admin", "root-password-1"); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(t.TempDir(), ".env")

	if _, err := bootstrapAdmin(st, "admin-second", envPath); err == nil {
		t.Fatal("a second site admin must be refused")
	}
	if _, err := os.Stat(envPath); !os.IsNotExist(err) {
		t.Fatalf("refused bootstrap must not write the env file (stat err %v)", err)
	}
	if _, found, _ := st.AccountByEmail("admin-second"); found {
		t.Fatal("refused bootstrap must not create the account")
	}
}

func TestAdminInitRejectsEnvUsernameMismatch(t *testing.T) {
	st := adminInitStore(t)
	envPath := filepath.Join(t.TempDir(), ".env")
	orig := "LEANKG_ADMIN_USERNAME=ops-other\n"
	adminInitWriteEnv(t, envPath, orig)

	if _, err := bootstrapAdmin(st, "admin-mismatch", envPath); err == nil {
		t.Fatal("env username that differs from --username must be refused")
	}
	if got := adminInitText(t, envPath); got != orig {
		t.Fatalf("refused bootstrap changed the env file: %q", got)
	}
}

func TestAdminInitRejectsShortEnvPasswordBeforeWriting(t *testing.T) {
	st := adminInitStore(t)
	envPath := filepath.Join(t.TempDir(), ".env")
	orig := "LEANKG_ADMIN_PASSWORD=abc\n"
	adminInitWriteEnv(t, envPath, orig)

	if _, err := bootstrapAdmin(st, "admin-short", envPath); err == nil {
		t.Fatal("a password below the policy minimum must be refused")
	}
	if got := adminInitText(t, envPath); got != orig {
		t.Fatalf("refused bootstrap changed the env file: %q", got)
	}
	if _, found, _ := st.AccountByEmail("admin-short"); found {
		t.Fatal("refused bootstrap must not create the account")
	}
}

func TestGenerateAdminPasswordIsRandomBase64URL(t *testing.T) {
	a, err := generateAdminPassword()
	if err != nil {
		t.Fatal(err)
	}
	b, err := generateAdminPassword()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two generated passwords must differ")
	}
	raw, err := base64.RawURLEncoding.DecodeString(a)
	if err != nil || len(raw) != 32 {
		t.Fatalf("password must decode to 32 random bytes (len %d, err %v)", len(raw), err)
	}
}

// TestAuthAdminInitCLI drives the real binary: the first run creates the
// account and prints no password; the second run is a no-op; with the env file
// removed, a third run fails non-zero.
func TestAuthAdminInitCLI(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")

	stdout, stderr, code := runCLI(t, "auth", "admin", "init", "--project", dir)
	if code != 0 {
		t.Fatalf("first init exit = %d, stderr: %s", code, stderr)
	}
	env, err := envfile.Load(envPath)
	if err != nil {
		t.Fatal(err)
	}
	password := env[adminPasswordKey]
	if password == "" {
		t.Fatal("first init must write LEANKG_ADMIN_PASSWORD")
	}
	if strings.Contains(stdout, password) || strings.Contains(stderr, password) {
		t.Fatal("init output must never print the password")
	}
	if info, _ := os.Stat(envPath); info.Mode().Perm() != 0o600 {
		t.Fatalf("env mode = %o, want 600", info.Mode().Perm())
	}

	// Widen the mode so the no-op run has to tighten it and say so.
	if err := os.Chmod(envPath, 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _, code = runCLI(t, "auth", "admin", "init", "--project", dir)
	if code != 0 || !strings.Contains(stdout, "already initialised") || !strings.Contains(stdout, "0600") {
		t.Fatalf("second init exit = %d, stdout: %s", code, stdout)
	}
	if info, _ := os.Stat(envPath); info.Mode().Perm() != 0o600 {
		t.Fatalf("env mode after no-op = %o, want 600", info.Mode().Perm())
	}

	if err := os.Remove(envPath); err != nil {
		t.Fatal(err)
	}
	_, stderr, code = runCLI(t, "auth", "admin", "init", "--project", dir)
	if code == 0 {
		t.Fatal("init with an account but no env credentials must exit non-zero")
	}
	if strings.Contains(stderr, password) {
		t.Fatal("failure output must never print the password")
	}
}
