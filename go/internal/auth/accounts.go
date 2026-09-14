// Accounts / orgs / memberships / resource ownership (Rust
// src/auth/accounts.rs parity).
//
// Registration creates an account plus a bootstrap org owned by it. Org
// permission checks are role-based on the ORG hierarchy
// (`owner` > `admin` > `member` > `viewer`) — a different vocabulary from the
// token/MCP Role (admin/contributor/viewer) that gates writes, which is why
// both hierarchies exist in this package.
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Org roles, ordered owner > admin > member > viewer (Rust accounts.rs).
const (
	OrgRoleOwner  = "owner"
	OrgRoleAdmin  = "admin"
	OrgRoleMember = "member"
	OrgRoleViewer = "viewer"
)

// Password verifier parameters. Rust hashed with argon2id (Argon2::default);
// the Go engine deliberately uses PBKDF2-HMAC-SHA256 from the standard library
// instead, because argon2 lives in golang.org/x/crypto and the module's
// dependency set is frozen. PBKDF2-HMAC-SHA256 at 600k iterations is the
// OWASP-recommended configuration and the encoded verifier carries its scheme,
// so an argon2 verifier could be added later without a format migration.
//
// ponytail: stdlib-only password KDF. Ceiling: weaker GPU resistance than
// argon2id. Upgrade path: add golang.org/x/crypto/argon2 and accept
// "argon2id$..." verifiers alongside "pbkdf2-sha256$...".
const (
	passwordScheme     = "pbkdf2-sha256"
	passwordIterations = 600_000
	passwordKeyLen     = 32
	passwordSaltLen    = 16
)

// HashPassword encodes a password verifier:
// `pbkdf2-sha256$<iterations>$<salt-hex>$<key-hex>`. A fresh random salt is
// generated per call, so equal passwords never share a verifier.
func HashPassword(password string) (string, error) {
	salt := make([]byte, passwordSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: password salt: %w", err)
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, passwordKeyLen)
	if err != nil {
		return "", fmt.Errorf("auth: password hash: %w", err)
	}
	return fmt.Sprintf("%s$%d$%s$%s", passwordScheme, passwordIterations,
		hex.EncodeToString(salt), hex.EncodeToString(key)), nil
}

// VerifyPassword reports whether password matches an encoded verifier. A
// malformed or unknown-scheme verifier is false, never an error: it must not
// authenticate (Rust verify_password).
func VerifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != passwordScheme {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations <= 0 {
		return false
	}
	salt, err := hex.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(parts[3])
	if err != nil || len(want) == 0 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// OrgRoleLevel ranks an org role (Rust role_sufficient's level closure).
// Unknown roles rank 0, below every real role.
func OrgRoleLevel(role string) int {
	switch role {
	case OrgRoleOwner:
		return 4
	case OrgRoleAdmin:
		return 3
	case OrgRoleMember:
		return 2
	case OrgRoleViewer:
		return 1
	}
	return 0
}

// RoleSufficient reports whether holding `actual` satisfies a `required` org
// role (Rust role_sufficient). It is the ORG hierarchy, not the token Role.
func RoleSufficient(actual, required string) bool {
	return OrgRoleLevel(actual) >= OrgRoleLevel(required)
}

// ValidOrgRole reports whether role is one of the four org roles.
func ValidOrgRole(role string) bool { return OrgRoleLevel(role) > 0 }

// OrgRoleToRole maps an org role onto the capability Role that gates write and
// admin actions: owners and org admins are Admins, members may write, viewers
// read only. Unknown org roles map to Viewer.
func OrgRoleToRole(orgRole string) Role {
	switch orgRole {
	case OrgRoleOwner, OrgRoleAdmin:
		return Admin
	case OrgRoleMember:
		return Contributor
	default:
		return Viewer
	}
}

// NormalizeEmail applies the storage normalization every account lookup uses
// (Rust register/verify_login).
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// newID returns an opaque 128-bit random id (Rust Uuid::new_v4 for account,
// org and token ids). Display form is un-hyphenated hex.
func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: generate id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// Register creates an account plus its bootstrap org owned by that account,
// with the account as the org's owner member. Duplicate emails and passwords
// shorter than 8 characters are rejected (Rust AccountStore::register).
func Register(st store.Backend, email, password, name string) (store.Account, error) {
	email = NormalizeEmail(email)
	if !strings.Contains(email, "@") {
		return store.Account{}, errors.New("invalid email")
	}
	if len(password) < 8 {
		return store.Account{}, errors.New("password must be at least 8 characters")
	}
	if _, found, err := st.AccountByEmail(email); err != nil {
		return store.Account{}, err
	} else if found {
		return store.Account{}, fmt.Errorf("account already exists: %s", email)
	}
	hash, err := HashPassword(password)
	if err != nil {
		return store.Account{}, err
	}
	now := time.Now().Unix()
	id, err := newID()
	if err != nil {
		return store.Account{}, err
	}
	account := store.Account{
		ID:           id,
		Email:        email,
		Name:         name,
		PasswordHash: hash,
		Status:       "active",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := st.AccountUpsert(account); err != nil {
		return store.Account{}, err
	}
	if _, err := CreateOrg(st, name+"'s org", account.ID); err != nil {
		return store.Account{}, err
	}
	return account, nil
}

// VerifyLogin checks credentials and returns the account. Email matching is
// case-insensitive; a wrong password and an unknown account are distinct
// errors (Rust verify_login).
func VerifyLogin(st store.Backend, email, password string) (store.Account, error) {
	account, found, err := st.AccountByEmail(NormalizeEmail(email))
	if err != nil {
		return store.Account{}, err
	}
	if !found {
		return store.Account{}, fmt.Errorf("no account for %s", email)
	}
	if !VerifyPassword(password, account.PasswordHash) {
		return store.Account{}, errors.New("invalid password")
	}
	return account, nil
}

// CreateOrg creates an org owned by ownerAccountID and adds that account as
// its owner member (Rust AccountStore::create_org).
func CreateOrg(st store.Backend, name, ownerAccountID string) (store.Org, error) {
	now := time.Now().Unix()
	id, err := newID()
	if err != nil {
		return store.Org{}, err
	}
	org := store.Org{
		ID:             id,
		Name:           name,
		OwnerAccountID: ownerAccountID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := st.OrgUpsert(org); err != nil {
		return store.Org{}, err
	}
	if err := st.OrgMembershipUpsert(store.OrgMember{
		OrgID:     org.ID,
		AccountID: ownerAccountID,
		Role:      OrgRoleOwner,
		JoinedAt:  now,
	}); err != nil {
		return store.Org{}, err
	}
	return org, nil
}

// AddOrgMember adds or re-roles an org member. The role must be one of the
// four org roles (Rust add_member).
func AddOrgMember(st store.Backend, orgID, accountID, role string) (store.OrgMember, error) {
	if !ValidOrgRole(role) {
		return store.OrgMember{}, fmt.Errorf("invalid role %q", role)
	}
	member := store.OrgMember{
		OrgID:     orgID,
		AccountID: accountID,
		Role:      role,
		JoinedAt:  time.Now().Unix(),
	}
	if err := st.OrgMembershipUpsert(member); err != nil {
		return store.OrgMember{}, err
	}
	return member, nil
}

// OrgRoleSufficient reports whether accountID holds required or a higher org
// role in orgID. A non-member is never sufficient (Rust org_role_sufficient).
func OrgRoleSufficient(st store.Backend, orgID, accountID, required string) (bool, error) {
	member, found, err := st.OrgMemberOf(orgID, accountID)
	if err != nil || !found {
		return false, err
	}
	return RoleSufficient(member.Role, required), nil
}

// ClaimResource records that ownerAccountID owns a resource (Rust
// AccountStore::claim_resource).
func ClaimResource(st store.Backend, resourceType, resourceID, ownerAccountID, orgID string) error {
	return st.ResourceClaim(resourceType, resourceID, ownerAccountID, orgID)
}

// IsResourceOwner reports whether accountID owns the resource (Rust
// AccountStore::is_resource_owner).
func IsResourceOwner(st store.Backend, resourceType, resourceID, accountID string) (bool, error) {
	return st.IsResourceOwner(resourceType, resourceID, accountID)
}
