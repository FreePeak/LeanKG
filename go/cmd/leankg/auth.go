package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/auth"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// cmdAuth dispatches the Rust `leankg auth` command (cli::AuthCommand):
// register (account + bootstrap org) plus the token verbs. Rust's flat
// `auth list-tokens` / `auth revoke` live under `auth token list|revoke` here.
func cmdAuth(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "auth: expected a subcommand (register | token create|list|revoke)")
		os.Exit(2)
	}
	switch args[0] {
	case "register":
		authRegister(args[1:])
	case "token":
		cmdAuthToken(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "auth: unknown subcommand %q (valid: register | token create|list|revoke)\n", args[0])
		os.Exit(2)
	}
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
