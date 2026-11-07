# Git history index

`import` with `action: "git"` (MCP `import` tool, `args.max_commits`) records a repository's commit history in the store, so
history can be searched (embedded commit messages), traversed (edges) and
ranked (churn, co-change). It is read-only over git (`GIT_OPTIONAL_LOCKS=0`).

## Data model (reuses `code_elements` and `relationships`, no schema change)

| Row | element_type | qualified_name | file_path | Notes |
|-----|--------------|----------------|-----------|-------|
| commit | `commit` | `git:commit:<sha>` | `""` | name = short sha + subject; content = message, author, date, numstat summary; metadata = sha, author, email, date, parents, files_changed, insertions, deletions |
| author | `author` | `git:author:<email>` | `""` | name = author name; content = name and email |
| file | `file` | `git:file:<path>` | `""` | endpoint for history edges; name = path |

The file rows carry an empty `file_path` on purpose. Re-indexing a file
deletes elements by `file_path` (`store.DeleteByFile`), and `leankg gc`
(`DeleteOrphanRelationships`) drops edges whose endpoint is not an element.
A history node with an empty path survives both, and the edges it owns
survive too.

| Edge | From | To | Metadata |
|------|------|----|----------|
| `modified_in` | `git:file:<path>` | `git:commit:<sha>` | sha, date, author, subject, insertions, deletions (denormalized so `history` never looks up commits) |
| `authored_by` | `git:commit:<sha>` | `git:author:<email>` | none |
| `co_changed_with` | `git:file:<a>` | `git:file:<b>` | count (written in both directions) |

## Import

- Walk: `git log --no-merges --numstat`, bounded by `max_commits` (default
  5000). A merge commit is skipped (its changes are already in its parents).
- Incremental: the last imported HEAD is stored in the kv table
  (namespace `githistory`, key `head`). When it is an ancestor of HEAD, only
  `head..HEAD` is walked and co-change counts are added to the stored counts.
  Otherwise (first run, history rewritten) the window is rebuilt and co-change
  counts are overwritten, not added.
- Co-change: commits touching more than 50 files contribute no pairs (they are
  still recorded in `modified_in`). Bounded worst case: window x 50^2 / 2 pairs.

## Query actions

| Action | Reads | Args |
|--------|-------|------|
| `history` | `modified_in` edges of `git:file:<path>` | `query` = path; `limit` |
| `cochange` | `co_changed_with` edges of `git:file:<path>` | `query` = path; `limit` |
| `churn` | live `git log --numstat` over the max-commits window | `args.since` (git date); `limit` |

These are query actions of the MCP `query` tool (engine: `internal/core/githistory.go`).

`churn` is computed from git, not the store: the store has no cheap way to
enumerate file rows without a full element scan.

## Ceilings

- Renames are not followed (`--no-renames`): a rename shows as delete + add.
- Co-change counts of pairs that vanish from a rebuilt window are not removed
  (there is no edge delete primitive); they stop changing.
- Commit, author and `git:file:` rows are stored as ordinary elements, and
  commits are embedded. A default search hides them (see below).

## Search visibility

History rows are answered by `history`, `cochange` and `churn`. The search
rungs (`search` ladder, `exact`, `fuzzy`, `semantic`) and `element` hide them
unless the caller sets `args.include_git: true`. The rule is one predicate
(`searchKeep` in `internal/core/core.go`): a row is history when its qualified
name starts with `git:`, which no code qualified name does. Keyword rungs read
a wider window (3x the limit, at least 50) before the predicate trims it to the
limit; the semantic rung pushes the predicate into the store's scoped vector and
keyword arms, and composes it with `args.scope`. A window cannot guarantee a
full answer when history rows outnumber it.
