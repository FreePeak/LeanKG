package auth

import "github.com/FreePeak/LeanKG/internal/store"

// Store is the part of the store contract the auth package uses: bearer
// tokens and the accounts, orgs and resource ownership they resolve against
// (plan section 3.3, rule 2). It is not Backend: the auth package never
// touches the audit chain, the index or the KV catalog.
type Store interface {
	store.Tokens
	store.Identity
}
