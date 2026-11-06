package main

import (
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/envfile"
	"github.com/FreePeak/LeanKG/internal/store"
)

// cmdAuth dispatches the Rust `leankg auth` command (cli::AuthCommand):
// register (account + bootstrap org) plus the token verbs. Rust's flat
// `auth list-tokens` / `auth revoke` live under `auth token list|revoke` here.
func cmdAuth(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "auth: expected a subcommand (register | token create|list|revoke | admin init)")
		os.Exit(2)
	}
	switch args[0] {
	case "register":
		authRegister(args[1:])
	case "token":
		cmdAuthToken(args[1:])
	case "admin":
		cmdAuthAdmin(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "auth: unknown subcommand %q (valid: register | token create|list|revoke | admin init)\n", args[0])
		os.Exit(2)
	}
}

func cmdAuthAdmin(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "auth admin: expected a subcommand (init)")
		os.Exit(2)
	}
	switch args[0] {
	case "init":
		authAdminInit(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "auth admin: unknown subcommand %q (valid: init)\n", args[0])
		os.Exit(2)
	}
}

// Env-file keys that carry the site-admin credentials.
const (
	adminUsernameKey = "LEANKG_ADMIN_USERNAME"
	adminPasswordKey = "LEANKG_ADMIN_PASSWORD"
)

// adminInitOutcome says what bootstrapAdmin did.
type adminInitOutcome int

const (
	adminCreatedGenerated adminInitOutcome = iota + 1
	adminCreatedFromEnv
	adminAlreadyInitialised
	adminAlreadyInitialisedTightened
)

// authAdminInit bootstraps the one site-admin account (`leankg auth admin
// init`). The plaintext password lives only in the env file; the store keeps a
// PBKDF2 verifier. The output never prints the password.
func authAdminInit(args []string) {
	fs := flag.NewFlagSet("auth admin init", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	username := fs.String("username", "admin", "site-admin username")
	envFile := fs.String("env-file", "", "file holding the admin credentials (default <project>/.env)")
	parseInterspersed("auth admin init", fs, args, 0)

	dir := resolveProjectDir(*project)
	envPath := *envFile
	if envPath == "" {
		envPath = filepath.Join(dir, ".env")
	}
	engine, err := openEngine(dir, store.RW)
	if err != nil {
		fatalJSON(err)
	}
	defer engine.Store().Close()
	if err := engine.Store().Migrate(); err != nil {
		fatalJSON(err)
	}

	key := auth.NormalizeEmail(*username)
	outcome, err := bootstrapAdmin(engine.Store(), key, envPath)
	if err != nil {
		fatalJSON(err)
	}
	switch outcome {
	case adminCreatedGenerated:
		fmt.Printf("Site admin %q created. Generated credentials written to %s (mode 0600); the password is not printed.\n", key, envPath)
	case adminCreatedFromEnv:
		fmt.Printf("Site admin %q created from the credentials in %s.\n", key, envPath)
	case adminAlreadyInitialised:
		fmt.Printf("Site admin %q is already initialised; the credentials in %s verify.\n", key, envPath)
	case adminAlreadyInitialisedTightened:
		fmt.Printf("Site admin %q is already initialised; the credentials in %s verify. Tightened the file mode to 0600 (it holds a plaintext password).\n", key, envPath)
	}
}

// bootstrapAdmin makes sure the site-admin account for username exists and that
// envPath holds its credentials. A missing account is created from the env
// file's LEANKG_ADMIN_PASSWORD, or from a generated password appended to the
// file. An existing account is left unchanged, and is reported as initialised
// only when the env credentials verify against it. Every refusal happens before
// any write, and the env file is written before the account so that a failed
// account insert leaves credentials a rerun can use.
func bootstrapAdmin(st store.Backend, username, envPath string) (adminInitOutcome, error) {
	key := auth.NormalizeEmail(username)
	env, err := envfile.Load(envPath)
	if err != nil {
		return 0, err
	}
	if name, ok := env[adminUsernameKey]; ok && auth.NormalizeEmail(name) != key {
		return 0, fmt.Errorf("%s names admin %q, not %q; pass --username to match", envPath, auth.NormalizeEmail(name), key)
	}
	envPassword, hasPassword := env[adminPasswordKey]

	_, exists, err := st.AccountByEmail(key)
	if err != nil {
		return 0, err
	}
	if exists {
		if hasPassword {
			if _, err := auth.VerifyAdminLogin(st, key, envPassword); err == nil {
				tightened, err := tightenEnvMode(envPath)
				if err != nil {
					return 0, err
				}
				if tightened {
					return adminAlreadyInitialisedTightened, nil
				}
				return adminAlreadyInitialised, nil
			}
		}
		return 0, fmt.Errorf("admin %q already exists and the credentials in %s do not verify it; nothing changed", key, envPath)
	}

	anyAdmin, err := st.AdminExists()
	if err != nil {
		return 0, err
	}
	if anyAdmin {
		return 0, fmt.Errorf("a site admin already exists; auth admin init creates only one (refusing %q)", key)
	}

	outcome := adminCreatedFromEnv
	password := envPassword
	if !hasPassword {
		outcome = adminCreatedGenerated
		if password, err = generateAdminPassword(); err != nil {
			return 0, err
		}
	}
	if err := auth.ValidatePassword(password); err != nil {
		return 0, fmt.Errorf("%s: %w", adminPasswordKey, err)
	}
	if err := envfile.AppendMissing(envPath, []envfile.Pair{
		{Key: adminUsernameKey, Value: key},
		{Key: adminPasswordKey, Value: password},
	}); err != nil {
		return 0, err
	}
	if _, err := auth.CreateSiteAdmin(st, key, password); err != nil {
		return 0, err
	}
	return outcome, nil
}

// tightenEnvMode sets the env file to 0600 and reports whether the mode changed.
// The file holds a plaintext password, so no run may leave it group- or
// world-readable.
func tightenEnvMode(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if info.Mode().Perm() == 0o600 {
		return false, nil
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return false, fmt.Errorf("chmod %s: %w", path, err)
	}
	return true, nil
}

// generateAdminPassword returns 32 random bytes as unpadded base64url.
func generateAdminPassword() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate admin password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func cmdAuthToken(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "auth token: expected a subcommand (create | list | revoke)")
		os.Exit(2)
	}
	switch args[0] {
	case "create":
		authTokenCreate(args[1:])
	case "list":
		authTokenList(args[1:])
	case "revoke":
		authTokenRevoke(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "auth token: unknown subcommand %q (valid: create | list | revoke)\n", args[0])
		os.Exit(2)
	}
}

// authRegister creates an account plus its bootstrap org (Rust
// AuthCommand::Register + auth_register, main.rs:4286-4299). The password is
// hashed at rest; the CLI never echoes it.
func authRegister(args []string) {
	fs := flag.NewFlagSet("auth register", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	email := fs.String("email", "", "account email (required)")
	password := fs.String("password", "", "account password (required, min 8 chars)")
	name := fs.String("name", "", "account display name (required)")
	parseInterspersed("auth register", fs, args, 0)
	for _, missing := range []struct{ flag, value string }{
		{"--email", *email}, {"--password", *password}, {"--name", *name},
	} {
		if missing.value == "" {
			fmt.Fprintf(os.Stderr, "auth register: %s is required\n", missing.flag)
			os.Exit(2)
		}
	}
	dir := resolveProjectDir(*project)
	engine, err := openEngine(dir, store.RW)
	if err != nil {
		fatalJSON(err)
	}
	defer engine.Store().Close()
	if err := engine.Store().Migrate(); err != nil {
		fatalJSON(err)
	}

	account, err := auth.Register(engine.Store(), *email, *password, *name)
	if err != nil {
		fatalJSON(err)
	}
	// Rust auth_register output shape.
	fmt.Println("Account registered:")
	fmt.Printf("  ID:    %s\n", account.ID)
	fmt.Printf("  Email: %s\n", account.Email)
	fmt.Printf("  Name:  %s\n", account.Name)
}

// authTokenCreate mints one DB-backed bearer token (Rust
// AuthCommand::Token + auth_issue_token). The plaintext is shown once; only
// its SHA-256 hash is persisted.
func authTokenCreate(args []string) {
	fs := flag.NewFlagSet("auth token create", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	name := fs.String("name", "", "token name (required)")
	role := fs.String("role", "viewer", "token role: admin | contributor | viewer")
	accountID := fs.String("account-id", "", "account this token acts for (from `auth register`; Rust --account-id)")
	orgID := fs.String("org-id", "", "org this token acts for (Rust --org-id)")
	scopes := fs.String("scopes", "", "comma-separated opaque scope names")
	ttl := fs.String("ttl", "", "token lifetime, e.g. 24h (default: never expires)")
	parseInterspersed("auth token create", fs, args, 0)
	if *name == "" {
		fmt.Fprintln(os.Stderr, "auth token create: --name is required")
		os.Exit(2)
	}
	var lifetime time.Duration
	if *ttl != "" {
		d, err := time.ParseDuration(*ttl)
		if err != nil {
			fmt.Fprintf(os.Stderr, "auth token create: invalid --ttl %q: %v\n", *ttl, err)
			os.Exit(2)
		}
		lifetime = d
	}
	req := auth.MintRequest{
		Name:      *name,
		Role:      *role,
		AccountID: *accountID,
		OrgID:     *orgID,
		Scopes:    splitScopes(*scopes),
		TTL:       lifetime,
	}
	dir := resolveProjectDir(*project)
	engine, err := openEngine(dir, store.RW)
	if err != nil {
		fatalJSON(err)
	}
	defer engine.Store().Close()
	// Minting needs the tokens table; a writer opens the schema first.
	if err := engine.Store().Migrate(); err != nil {
		fatalJSON(err)
	}

	token, row, err := auth.Mint(engine.Store(), req)
	if err != nil {
		fatalJSON(err)
	}
	// Rust auth_issue_token output shape.
	fmt.Println("Access token issued:")
	fmt.Printf("  ID:    %s\n", row.ID)
	fmt.Printf("  Role:  %s\n", row.Role)
	fmt.Println()
	fmt.Println("IMPORTANT: Save this token - it will not be shown again:")
	fmt.Printf("  %s\n", token)
}

// authTokenList prints token rows, revoked ones included (Rust
// AuthCommand::ListTokens + auth_list_tokens). --account-id filters by the
// token's account label, matching Rust's account-scoped listing.
func authTokenList(args []string) {
	fs := flag.NewFlagSet("auth token list", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	accountID := fs.String("account-id", "", "only tokens issued for this account (Rust --account-id)")
	parseInterspersed("auth token list", fs, args, 0)
	dir := resolveProjectDir(*project)
	engine, err := openEngine(dir, store.RO)
	if err != nil {
		fatalJSON(err)
	}
	defer engine.Store().Close()

	tokens, err := engine.Store().TokenList()
	if err != nil {
		fatalJSON(err)
	}
	if *accountID != "" {
		kept := make([]store.Token, 0, len(tokens))
		for _, t := range tokens {
			if t.AccountID == *accountID {
				kept = append(kept, t)
			}
		}
		tokens = kept
	}
	switch {
	case len(tokens) == 0 && *accountID != "":
		fmt.Printf("No access tokens for account %s.\n", *accountID)
		return
	case len(tokens) == 0:
		fmt.Println("No access tokens.")
		return
	case *accountID != "":
		fmt.Printf("Access tokens for %s:\n", *accountID)
	default:
		fmt.Println("Access tokens:")
	}
	for _, t := range tokens {
		fmt.Printf("  ID: %s  Name: %s  Role: %s  revoked: %t\n", t.ID, t.Name, t.Role, t.RevokedAt != 0)
	}
}

// authTokenRevoke soft-revokes one token by id (Rust AuthCommand::Revoke +
// auth_revoke_token). Revocation is idempotent: an unknown or already-revoked
// id reports that state instead of failing.
func authTokenRevoke(args []string) {
	fs := flag.NewFlagSet("auth token revoke", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	tokenID := fs.String("token-id", "", "token id to revoke (from `auth token list`)")
	parseInterspersed("auth token revoke", fs, args, 0)
	if *tokenID == "" {
		fmt.Fprintln(os.Stderr, "auth token revoke: --token-id is required")
		os.Exit(2)
	}
	dir := resolveProjectDir(*project)
	engine, err := openEngine(dir, store.RW)
	if err != nil {
		fatalJSON(err)
	}
	defer engine.Store().Close()

	tokens, err := engine.Store().TokenList()
	if err != nil {
		fatalJSON(err)
	}
	found := false
	revoked := false
	for _, t := range tokens {
		if t.ID != *tokenID {
			continue
		}
		found = true
		revoked = t.RevokedAt != 0
		break
	}
	if !found {
		fmt.Printf("Access token '%s' not found.\n", *tokenID)
		return
	}
	if revoked {
		fmt.Printf("Access token '%s' already revoked.\n", *tokenID)
		return
	}
	if err := engine.Store().TokenRevoke(*tokenID, time.Now().Unix()); err != nil {
		fatalJSON(err)
	}
	fmt.Printf("Access token '%s' revoked.\n", *tokenID)
}

// splitScopes parses the comma-separated --scopes value, dropping blanks.
func splitScopes(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
