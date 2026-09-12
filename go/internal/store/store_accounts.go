// Enterprise auth storage: accounts, orgs, org memberships, team members and
// resource ownership (Rust src/auth/accounts.rs + src/db/pg/migrations/
// 004_auth.sql parity). Permission POLICY — the role hierarchy, register and
// login, bootstrap orgs, ownership precedence — lives in internal/auth; this
// file is typed table access over the SQLite backend only.
//
// Deliberate divergence: Rust's Cozo layout keyed resource_ownership on every
// column, so re-claiming a resource with a different org_id appended a second
// row. Here (resource_type, resource_id, owner_account_id) is the primary key,
// so a re-claim updates the row; IsResourceOwner observes the same answer
// either way.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Account is one registered account. PasswordHash is an encoded password
// verifier (see internal/auth), never a plaintext or a reversible form.
type Account struct {
	ID           string `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	PasswordHash string `json:"password_hash"`
	Status       string `json:"status"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

// Org is a tenant owned by exactly one account (Rust orgs.owner_account_id).
type Org struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	OwnerAccountID string `json:"owner_account_id"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
}

// OrgMember is one (org, account) membership row. Role is an ORG role
// ("owner" | "admin" | "member" | "viewer") — a different vocabulary from the
// token/MCP Role, and the reason internal/auth keeps both hierarchies.
type OrgMember struct {
	OrgID     string `json:"org_id"`
	AccountID string `json:"account_id"`
	Role      string `json:"role"`
	JoinedAt  int64  `json:"joined_at"`
}

// TeamMember is one (team, account) membership row. team_id is an opaque
// label: the Go engine has no teams table (the Rust `teams` table came from
// 001_schema, which was not part of this port), so nothing resolves it.
type TeamMember struct {
	TeamID    string `json:"team_id"`
	AccountID string `json:"account_id"`
	Role      string `json:"role"`
	JoinedAt  int64  `json:"joined_at"`
}

const accountCols = `id, email, name, password_hash, status, created_at, updated_at`
const orgCols = `id, name, owner_account_id, created_at, updated_at`
const orgMemberCols = `org_id, account_id, role, joined_at`

// AccountUpsert inserts or replaces an account keyed by ID. The write replaces
// every column (Rust upsert_account's :put accounts semantics).
func (s *Store) AccountUpsert(a Account) error {
	if a.ID == "" || a.Email == "" {
		return fmt.Errorf("store: account upsert: id and email are required")
	}
	_, err := s.db.Exec(`INSERT INTO accounts (`+accountCols+`)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			email = excluded.email, name = excluded.name,
			password_hash = excluded.password_hash, status = excluded.status,
			created_at = excluded.created_at, updated_at = excluded.updated_at`,
		a.ID, a.Email, a.Name, a.PasswordHash, a.Status, a.CreatedAt, a.UpdatedAt)
	if err != nil {
		return fmt.Errorf("store: account upsert %s: %w", a.ID, err)
	}
	return nil
}

// AccountByEmail resolves an account by its (exact, already-normalized) email.
// Callers normalize with strings.ToLower(strings.TrimSpace(...)) first — the
// UNIQUE index is on the stored form.
func (s *Store) AccountByEmail(email string) (Account, bool, error) {
	row := s.db.QueryRow(`SELECT `+accountCols+` FROM accounts WHERE email = ?`, email)
	a, err := scanAccount(row.Scan)
	if err != nil {
		if isNoRows(err) {
			return Account{}, false, nil
		}
		return Account{}, false, fmt.Errorf("store: account by email: %w", err)
	}
	return a, true, nil
}

// OrgUpsert inserts or replaces an org keyed by ID.
func (s *Store) OrgUpsert(o Org) error {
	if o.ID == "" || o.OwnerAccountID == "" {
		return fmt.Errorf("store: org upsert: id and owner_account_id are required")
	}
	_, err := s.db.Exec(`INSERT INTO orgs (`+orgCols+`)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name, owner_account_id = excluded.owner_account_id,
			created_at = excluded.created_at, updated_at = excluded.updated_at`,
		o.ID, o.Name, o.OwnerAccountID, o.CreatedAt, o.UpdatedAt)
	if err != nil {
		return fmt.Errorf("store: org upsert %s: %w", o.ID, err)
	}
	return nil
}

// OrgByID resolves one org by id.
func (s *Store) OrgByID(id string) (Org, bool, error) {
	row := s.db.QueryRow(`SELECT `+orgCols+` FROM orgs WHERE id = ?`, id)
	o, err := scanOrg(row.Scan)
	if err != nil {
		if isNoRows(err) {
			return Org{}, false, nil
		}
		return Org{}, false, fmt.Errorf("store: org by id: %w", err)
	}
	return o, true, nil
}

// OrgsByOwner lists the orgs owned by an account (the accounts.rs bootstrap-org
// and "my orgs" lookup; Rust reached this through a raw owner_account_id query).
func (s *Store) OrgsByOwner(ownerAccountID string) ([]Org, error) {
	rows, err := s.db.Query(`SELECT `+orgCols+` FROM orgs WHERE owner_account_id = ? ORDER BY created_at, id`, ownerAccountID)
	if err != nil {
		return nil, fmt.Errorf("store: orgs by owner: %w", err)
	}
	defer rows.Close()
	var out []Org
	for rows.Next() {
		o, err := scanOrg(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("store: orgs by owner scan: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// OrgMembershipUpsert adds or re-roles an org member (Rust upsert_member:
// UNIQUE (org_id, account_id) makes a re-add an update, never a duplicate).
func (s *Store) OrgMembershipUpsert(m OrgMember) error {
	if m.OrgID == "" || m.AccountID == "" {
		return fmt.Errorf("store: org membership upsert: org_id and account_id are required")
	}
	_, err := s.db.Exec(`INSERT INTO org_memberships (`+orgMemberCols+`)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(org_id, account_id) DO UPDATE SET
			role = excluded.role, joined_at = excluded.joined_at`,
		m.OrgID, m.AccountID, m.Role, m.JoinedAt)
	if err != nil {
		return fmt.Errorf("store: org membership upsert %s/%s: %w", m.OrgID, m.AccountID, err)
	}
	return nil
}

// OrgMemberOf resolves one membership row.
func (s *Store) OrgMemberOf(orgID, accountID string) (OrgMember, bool, error) {
	row := s.db.QueryRow(`SELECT `+orgMemberCols+` FROM org_memberships WHERE org_id = ? AND account_id = ?`, orgID, accountID)
	m, err := scanOrgMember(row.Scan)
	if err != nil {
		if isNoRows(err) {
			return OrgMember{}, false, nil
		}
		return OrgMember{}, false, fmt.Errorf("store: org member: %w", err)
	}
	return m, true, nil
}

// OrgMembers lists every member of an org.
func (s *Store) OrgMembers(orgID string) ([]OrgMember, error) {
	rows, err := s.db.Query(`SELECT `+orgMemberCols+` FROM org_memberships WHERE org_id = ? ORDER BY joined_at, account_id`, orgID)
	if err != nil {
		return nil, fmt.Errorf("store: org members: %w", err)
	}
	defer rows.Close()
	var out []OrgMember
	for rows.Next() {
		m, err := scanOrgMember(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("store: org members scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// TeamMemberUpsert adds or re-roles a team member (same uniqueness contract as
// org memberships: UNIQUE (team_id, account_id)).
func (s *Store) TeamMemberUpsert(m TeamMember) error {
	if m.TeamID == "" || m.AccountID == "" {
		return fmt.Errorf("store: team member upsert: team_id and account_id are required")
	}
	_, err := s.db.Exec(`INSERT INTO team_members (team_id, account_id, role, joined_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(team_id, account_id) DO UPDATE SET
			role = excluded.role, joined_at = excluded.joined_at`,
		m.TeamID, m.AccountID, m.Role, m.JoinedAt)
	if err != nil {
		return fmt.Errorf("store: team member upsert %s/%s: %w", m.TeamID, m.AccountID, err)
	}
	return nil
}

// TeamMemberOf resolves one team membership row.
func (s *Store) TeamMemberOf(teamID, accountID string) (TeamMember, bool, error) {
	row := s.db.QueryRow(`SELECT team_id, account_id, role, joined_at FROM team_members WHERE team_id = ? AND account_id = ?`, teamID, accountID)
	m, err := scanTeamMember(row.Scan)
	if err != nil {
		if isNoRows(err) {
			return TeamMember{}, false, nil
		}
		return TeamMember{}, false, fmt.Errorf("store: team member: %w", err)
	}
	return m, true, nil
}

// TeamMembers lists every member of a team.
func (s *Store) TeamMembers(teamID string) ([]TeamMember, error) {
	rows, err := s.db.Query(`SELECT team_id, account_id, role, joined_at FROM team_members WHERE team_id = ? ORDER BY joined_at, account_id`, teamID)
	if err != nil {
		return nil, fmt.Errorf("store: team members: %w", err)
	}
	defer rows.Close()
	var out []TeamMember
	for rows.Next() {
		m, err := scanTeamMember(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("store: team members scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ResourceClaim records that ownerAccountID owns a resource. orgID "" means
// unset (NULL): Rust wrote an empty string for its `None`, which is the same
// "unset" observation on read.
func (s *Store) ResourceClaim(resourceType, resourceID, ownerAccountID, orgID string) error {
	if resourceType == "" || resourceID == "" || ownerAccountID == "" {
		return fmt.Errorf("store: resource claim: resource_type, resource_id and owner_account_id are required")
	}
	_, err := s.db.Exec(`INSERT INTO resource_ownership
		(resource_type, resource_id, owner_account_id, org_id, created_at)
		VALUES (?, ?, ?, NULLIF(?, ''), ?)
		ON CONFLICT(resource_type, resource_id, owner_account_id) DO UPDATE SET
			org_id = excluded.org_id`,
		resourceType, resourceID, ownerAccountID, orgID, nowEpoch())
	if err != nil {
		return fmt.Errorf("store: resource claim %s/%s: %w", resourceType, resourceID, err)
	}
	return nil
}

// IsResourceOwner reports whether accountID owns the resource. Ownership is
// per-account: another account claiming the same resource is a separate row
// and never makes this one true (Rust is_resource_owner filters on
// owner_account_id too).
func (s *Store) IsResourceOwner(resourceType, resourceID, accountID string) (bool, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM resource_ownership
		WHERE resource_type = ? AND resource_id = ? AND owner_account_id = ?`,
		resourceType, resourceID, accountID).Scan(&n); err != nil {
		return false, fmt.Errorf("store: is resource owner: %w", err)
	}
	return n > 0, nil
}

func scanAccount(scan func(dest ...any) error) (Account, error) {
	var a Account
	err := scan(&a.ID, &a.Email, &a.Name, &a.PasswordHash, &a.Status, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

func scanOrg(scan func(dest ...any) error) (Org, error) {
	var o Org
	err := scan(&o.ID, &o.Name, &o.OwnerAccountID, &o.CreatedAt, &o.UpdatedAt)
	return o, err
}

func scanOrgMember(scan func(dest ...any) error) (OrgMember, error) {
	var m OrgMember
	err := scan(&m.OrgID, &m.AccountID, &m.Role, &m.JoinedAt)
	return m, err
}

func scanTeamMember(scan func(dest ...any) error) (TeamMember, error) {
	var m TeamMember
	err := scan(&m.TeamID, &m.AccountID, &m.Role, &m.JoinedAt)
	return m, err
}

// isNoRows reports sql.ErrNoRows through either backend's error wrapping.
func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// nowEpoch is the epoch-seconds clock for legacy auth timestamps.
func nowEpoch() int64 { return time.Now().Unix() }
