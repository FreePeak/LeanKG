// PostgreSQL account-store methods (mirror of store_accounts.go over pgx):
// accounts, orgs, org memberships, team members and resource ownership.
// Semantics are identical to the SQLite implementation; the only differences
// are placeholders ($n), jsonb-free TEXT/BIGINT columns and NULLIF for the
// unset org label.
package store

import "fmt"

// AccountUpsert inserts or replaces an account keyed by ID.
func (s *PGStore) AccountUpsert(a Account) error {
	if a.ID == "" || a.Email == "" {
		return fmt.Errorf("store: account upsert: id and email are required")
	}
	_, err := s.pool.Exec(pgCtx, `INSERT INTO accounts (`+accountCols+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO UPDATE SET
			email = excluded.email, name = excluded.name,
			password_hash = excluded.password_hash, status = excluded.status,
			created_at = excluded.created_at, updated_at = excluded.updated_at`,
		a.ID, a.Email, a.Name, a.PasswordHash, a.Status, a.CreatedAt, a.UpdatedAt)
	if err != nil {
		return fmt.Errorf("store: account upsert %s: %w", a.ID, err)
	}
	return nil
}

// AccountByEmail resolves an account by its exact (already normalized) email.
func (s *PGStore) AccountByEmail(email string) (Account, bool, error) {
	row := s.pool.QueryRow(pgCtx, `SELECT `+accountCols+` FROM accounts WHERE email = $1`, email)
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
func (s *PGStore) OrgUpsert(o Org) error {
	if o.ID == "" || o.OwnerAccountID == "" {
		return fmt.Errorf("store: org upsert: id and owner_account_id are required")
	}
	_, err := s.pool.Exec(pgCtx, `INSERT INTO orgs (`+orgCols+`)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (id) DO UPDATE SET
			name = excluded.name, owner_account_id = excluded.owner_account_id,
			created_at = excluded.created_at, updated_at = excluded.updated_at`,
		o.ID, o.Name, o.OwnerAccountID, o.CreatedAt, o.UpdatedAt)
	if err != nil {
		return fmt.Errorf("store: org upsert %s: %w", o.ID, err)
	}
	return nil
}

// OrgByID resolves one org by id.
func (s *PGStore) OrgByID(id string) (Org, bool, error) {
	row := s.pool.QueryRow(pgCtx, `SELECT `+orgCols+` FROM orgs WHERE id = $1`, id)
	o, err := scanOrg(row.Scan)
	if err != nil {
		if isNoRows(err) {
			return Org{}, false, nil
		}
		return Org{}, false, fmt.Errorf("store: org by id: %w", err)
	}
	return o, true, nil
}

// OrgsByOwner lists the orgs owned by an account.
func (s *PGStore) OrgsByOwner(ownerAccountID string) ([]Org, error) {
	rows, err := s.pool.Query(pgCtx, `SELECT `+orgCols+` FROM orgs WHERE owner_account_id = $1 ORDER BY created_at, id`, ownerAccountID)
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

// OrgMembershipUpsert adds or re-roles an org member.
func (s *PGStore) OrgMembershipUpsert(m OrgMember) error {
	if m.OrgID == "" || m.AccountID == "" {
		return fmt.Errorf("store: org membership upsert: org_id and account_id are required")
	}
	_, err := s.pool.Exec(pgCtx, `INSERT INTO org_memberships (`+orgMemberCols+`)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (org_id, account_id) DO UPDATE SET
			role = excluded.role, joined_at = excluded.joined_at`,
		m.OrgID, m.AccountID, m.Role, m.JoinedAt)
	if err != nil {
		return fmt.Errorf("store: org membership upsert %s/%s: %w", m.OrgID, m.AccountID, err)
	}
	return nil
}

// OrgMemberOf resolves one membership row.
func (s *PGStore) OrgMemberOf(orgID, accountID string) (OrgMember, bool, error) {
	row := s.pool.QueryRow(pgCtx, `SELECT `+orgMemberCols+` FROM org_memberships WHERE org_id = $1 AND account_id = $2`, orgID, accountID)
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
func (s *PGStore) OrgMembers(orgID string) ([]OrgMember, error) {
	rows, err := s.pool.Query(pgCtx, `SELECT `+orgMemberCols+` FROM org_memberships WHERE org_id = $1 ORDER BY joined_at, account_id`, orgID)
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

// TeamMemberUpsert adds or re-roles a team member.
func (s *PGStore) TeamMemberUpsert(m TeamMember) error {
	if m.TeamID == "" || m.AccountID == "" {
		return fmt.Errorf("store: team member upsert: team_id and account_id are required")
	}
	_, err := s.pool.Exec(pgCtx, `INSERT INTO team_members (team_id, account_id, role, joined_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (team_id, account_id) DO UPDATE SET
			role = excluded.role, joined_at = excluded.joined_at`,
		m.TeamID, m.AccountID, m.Role, m.JoinedAt)
	if err != nil {
		return fmt.Errorf("store: team member upsert %s/%s: %w", m.TeamID, m.AccountID, err)
	}
	return nil
}

// TeamMemberOf resolves one team membership row.
func (s *PGStore) TeamMemberOf(teamID, accountID string) (TeamMember, bool, error) {
	row := s.pool.QueryRow(pgCtx, `SELECT team_id, account_id, role, joined_at FROM team_members WHERE team_id = $1 AND account_id = $2`, teamID, accountID)
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
func (s *PGStore) TeamMembers(teamID string) ([]TeamMember, error) {
	rows, err := s.pool.Query(pgCtx, `SELECT team_id, account_id, role, joined_at FROM team_members WHERE team_id = $1 ORDER BY joined_at, account_id`, teamID)
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

// ResourceClaim records that ownerAccountID owns a resource; orgID "" means
// unset (NULL).
func (s *PGStore) ResourceClaim(resourceType, resourceID, ownerAccountID, orgID string) error {
	if resourceType == "" || resourceID == "" || ownerAccountID == "" {
		return fmt.Errorf("store: resource claim: resource_type, resource_id and owner_account_id are required")
	}
	_, err := s.pool.Exec(pgCtx, `INSERT INTO resource_ownership
		(resource_type, resource_id, owner_account_id, org_id, created_at)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5)
		ON CONFLICT (resource_type, resource_id, owner_account_id) DO UPDATE SET
			org_id = excluded.org_id`,
		resourceType, resourceID, ownerAccountID, orgID, nowEpoch())
	if err != nil {
		return fmt.Errorf("store: resource claim %s/%s: %w", resourceType, resourceID, err)
	}
	return nil
}

// IsResourceOwner reports whether accountID owns the resource.
func (s *PGStore) IsResourceOwner(resourceType, resourceID, accountID string) (bool, error) {
	var n int
	if err := s.pool.QueryRow(pgCtx, `SELECT COUNT(*) FROM resource_ownership
		WHERE resource_type = $1 AND resource_id = $2 AND owner_account_id = $3`,
		resourceType, resourceID, accountID).Scan(&n); err != nil {
		return false, fmt.Errorf("store: is resource owner: %w", err)
	}
	return n > 0, nil
}
