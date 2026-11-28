package ccmemory

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/internal/flock"
	"github.com/FreePeak/LeanKG/internal/memory"
)

// CentralBankPrefix marks the only banks sync may ever plan writes against.
const CentralBankPrefix = "cc-"

// ArchiveBank keeps a copy of every row sync deletes because its memory file
// vanished. It has no cc- prefix, so sync, recall and the session hook never read it.
const ArchiveBank = "ccarchive"

// Key identifies one central memory row: the bank it lives in and its document id.
type Key struct {
	Bank  string
	DocID string
}

// Plan is the set of writes that makes the actual cc- banks equal the desired set.
// Upserts and Deletes are sorted by bank, then document id.
type Plan struct {
	Upserts         []Key
	Deletes         []Key
	Archives        []Key // the Deletes that keep a copy in ArchiveBank first
	DeletesWithheld bool
}

// BuildPlan diffs desired (key to source hash) against actual (key to source hash).
// Only cc- banks take part in the diff, so Deletes never name a row outside them.
// An empty desired set against a non-empty actual set deletes nothing and sets
// DeletesWithheld, unless rebuild is requested. Rebuild deletes every actual key
// and upserts every desired key regardless of hash.
func BuildPlan(desired, actual map[Key]string, rebuild bool) Plan {
	actual = centralOnly(actual)

	if rebuild {
		return Plan{Upserts: sortedKeys(desired), Deletes: sortedKeys(actual)}
	}
	if len(desired) == 0 {
		return Plan{DeletesWithheld: len(actual) > 0}
	}

	var plan Plan
	for key, hash := range desired {
		if have, ok := actual[key]; !ok || have != hash {
			plan.Upserts = append(plan.Upserts, key)
		}
	}
	for key := range actual {
		if _, ok := desired[key]; !ok {
			plan.Deletes = append(plan.Deletes, key)
		}
	}
	sortKeys(plan.Upserts)
	sortKeys(plan.Deletes)
	return plan
}

func centralOnly(actual map[Key]string) map[Key]string {
	out := make(map[Key]string, len(actual))
	for key, hash := range actual {
		if strings.HasPrefix(key.Bank, CentralBankPrefix) {
			out[key] = hash
		}
	}
	return out
}

func sortedKeys(set map[Key]string) []Key {
	var keys []Key
	for key := range set {
		keys = append(keys, key)
	}
	sortKeys(keys)
	return keys
}

func sortKeys(keys []Key) {
	slices.SortFunc(keys, func(a, b Key) int {
		return cmp.Or(strings.Compare(a.Bank, b.Bank), strings.Compare(a.DocID, b.DocID))
	})
}

// syncLockFile and scrubFile live directly under the central root.
const (
	syncLockFile = "cc-sync.lock"
	scrubFile    = "scrub.txt"
)

// ErrLockHeld means another sync holds the lock and the caller did not wait.
var ErrLockHeld = errors.New("ccmemory: sync lock is held by another run")

// SyncOptions selects the projects root to read and the central root to write.
type SyncOptions struct {
	Projects    string // Claude Code projects root
	CentralRoot string // memory root: holds the lock file, scrub.txt and the banks
	DryRun      bool   // compute and report the plan; write nothing
	Rebuild     bool   // delete every cc- row, then sync from scratch
	WaitForLock bool   // block for the lock instead of failing with ErrLockHeld
	OnlySlug    string // limit the run to one project slug; empty means every project
}

// SyncResult is the outcome of one sync run, in the JSON shape of plan 6.4.
type SyncResult struct {
	Root            string     `json:"root"`
	Projects        string     `json:"projects"`
	Documents       int        `json:"documents"`
	Upserted        int        `json:"upserted"`
	Deleted         int        `json:"deleted"`
	Skipped         int        `json:"skipped"`
	DeletesWithheld bool       `json:"deletes_withheld"`
	FailedProjects  []string   `json:"-"` // human summary only; JSON shows deletes_withheld and skipped
	Banks           []SyncBank `json:"banks"`
}

// SyncBank reports one cc- bank: the project it serves (empty for the global
// bank) and the number of documents it holds after the run.
type SyncBank struct {
	Bank    string `json:"bank"`
	Project string `json:"project"`
	Rows    int    `json:"rows"`
}

// compactCentral is the post-apply compaction of the cc- banks. It runs under the
// sync lock: compaction rewrites the bank files, and a locked sync that appended
// to them in between would lose its rows. A test replaces it to observe the lock.
var compactCentral = func(ctx context.Context, mem *memory.Memory) error {
	return mem.CompactPrefix(ctx, CentralBankPrefix)
}

// renderVersion is part of every source hash. Bump it when the row mapping
// changes, so the next run rewrites every row.
const renderVersion = "cc-row-v1"

// claudeCodeSource is the Entry.Source of every row sync writes.
const claudeCodeSource = "claude-code-memory"

// Sync makes the cc- banks under mem equal to the memory files under
// opts.Projects. It installs the deny-list, takes the sync lock, plans against
// the stored rows and applies one retain and one delete per bank. A ctx
// cancelled during the file scan fails the run before any plan is made, so
// nothing is written or deleted. Once the plan is applied, a cancelled ctx stops
// between banks; each bank's batch is complete or absent.
func Sync(ctx context.Context, mem *memory.Memory, opts SyncOptions) (SyncResult, error) {
	if err := InstallDenyList(opts.CentralRoot); err != nil {
		return SyncResult{Root: opts.CentralRoot, Projects: opts.Projects}, err
	}
	res, _, err := syncPass(ctx, mem, opts, nil)
	return res, err
}

// DenyListPath is the deny-list file under the central root.
func DenyListPath(central string) string {
	return filepath.Join(central, scrubFile)
}

// InstallDenyList loads the central deny-list and installs it process-wide.
// Every read and write of a sync, migrate, hook, serve or recall run is masked
// by it, so the callers install it before they read the central root. A file
// that does not load leaves the installed list unchanged and returns the error.
// The error names the file and the reason only: its text reaches stderr.
func InstallDenyList(central string) error {
	path := DenyListPath(central)
	patterns, digest, err := memory.LoadScrubFile(path)
	if err != nil {
		reason := err
		var pe *fs.PathError
		if errors.As(err, &pe) {
			reason = pe.Err // the path error carries the full path; keep the OS reason
		}
		return fmt.Errorf("%s: %w", filepath.Base(path), reason)
	}
	memory.SetScrubPatterns(patterns, digest)
	return nil
}

// syncState is what one sync pass read: the discovery limited to the scope,
// the desired hash of every document and the slug each project bank serves.
type syncState struct {
	disc      Discovery
	desired   map[Key]string
	projectOf map[string]string
}

// syncPass is Sync, without the deny-list install, which the caller has done.
// Once the plan is applied it compacts the cc- banks and then runs after, when
// not nil, both under the lock and not on a dry run.
func syncPass(ctx context.Context, mem *memory.Memory, opts SyncOptions, after func(syncState) error) (SyncResult, syncState, error) {
	res := SyncResult{Root: opts.CentralRoot, Projects: opts.Projects}
	if err := ctx.Err(); err != nil {
		return res, syncState{}, err
	}
	// Read before the lock and any write: a list that cannot be read stops the run.
	ex, err := loadExcludeList()
	if err != nil {
		return res, syncState{}, err
	}
	// A dry run only reads, so it takes no lock and creates no file under the root.
	if !opts.DryRun {
		release, err := takeLock(opts)
		if err != nil {
			return res, syncState{}, err
		}
		defer release()
	}

	// A scan that failed or was cancelled says nothing about the stored rows, so
	// the run ends here with deletes withheld.
	disc, err := discover(ctx, opts.Projects, ex)
	if err != nil {
		res.DeletesWithheld = true
		return res, syncState{}, err
	}
	disc = scopeDiscovery(disc, opts.Projects, opts.OnlySlug)
	projectOf := map[string]string{}
	hashes, docs := desiredSet(disc.Docs, projectOf)
	rowSlug := map[Key]string{}
	rows, err := actualRows(mem, opts.OnlySlug)
	if err != nil {
		return res, syncState{}, err
	}
	actual := splitRows(rows, projectOf, rowSlug)
	// Rows of excluded projects are deleted whatever the scan saw, so they are
	// taken out of the diff before it: a withheld delete must not keep them.
	purged := takeExcludedRows(actual, rowSlug, ex)
	archivedExcluded, err := excludedArchiveKeys(mem, opts.OnlySlug, ex)
	if err != nil {
		return res, syncState{}, err
	}
	purged = append(purged, archivedExcluded...)
	// A scoped rebuild is the explicit forget of one project: it archives nothing.
	forget := opts.Rebuild && opts.OnlySlug != ""
	plan := withholdFailed(BuildPlan(hashes, actual, opts.Rebuild), rowSlug, disc, opts.Rebuild)
	// Archives are chosen before the purged rows join the deletes, so an
	// excluded row can never be archived.
	plan.Archives = archiveKeys(plan.Deletes, hashes, forget)
	plan.Deletes = append(plan.Deletes, purged...)
	sortKeys(plan.Deletes)

	res.Documents = len(disc.Docs)
	res.Upserted = len(plan.Upserts)
	res.Deleted = len(plan.Deletes)
	res.Skipped = len(disc.Skipped)
	res.DeletesWithheld = plan.DeletesWithheld
	res.FailedProjects = append([]string{}, disc.FailedSlugs...)
	res.Banks = bankSummary(actual, plan, projectOf)
	st := syncState{disc: disc, desired: hashes, projectOf: projectOf}
	if opts.DryRun {
		return res, st, nil
	}
	if err := apply(ctx, mem, plan, hashes, docs, rows); err != nil {
		return res, st, err
	}
	if err := compactCentral(ctx, mem); err != nil {
		return res, st, err
	}
	if after != nil {
		if err := after(st); err != nil {
			return res, st, err
		}
	}
	return res, st, nil
}

// scopeDiscovery keeps only the documents and skipped files of slug. An empty
// slug keeps everything.
func scopeDiscovery(disc Discovery, projectsRoot, slug string) Discovery {
	if slug == "" {
		return disc
	}
	scoped := Discovery{RootMissing: disc.RootMissing, Seen: disc.Seen}
	for _, d := range disc.Docs {
		if d.Slug == slug {
			scoped.Docs = append(scoped.Docs, d)
		}
	}
	for _, s := range disc.FailedSlugs {
		if s == slug {
			scoped.FailedSlugs = append(scoped.FailedSlugs, s)
		}
	}
	for _, s := range disc.Skipped {
		if slugOf(projectsRoot, s.Path) == slug {
			scoped.Skipped = append(scoped.Skipped, s)
		}
	}
	return scoped
}

// slugOf returns the project slug of a memory path: its first element under
// projectsRoot.
func slugOf(projectsRoot, path string) string {
	rel, err := filepath.Rel(projectsRoot, path)
	if err != nil {
		return ""
	}
	slug, _, _ := strings.Cut(rel, string(filepath.Separator))
	return slug
}

// takeLock returns the release func for the sync lock. Without WaitForLock a
// busy lock is ErrLockHeld.
func takeLock(opts SyncOptions) (func(), error) {
	path := filepath.Join(opts.CentralRoot, syncLockFile)
	if opts.WaitForLock {
		return flock.Lock(path)
	}
	release, err := flock.TryLock(path)
	if errors.Is(err, flock.ErrHeld) {
		return nil, ErrLockHeld
	}
	return release, err
}

// desiredSet maps each discovered document to its key and source hash, and
// keeps the document for the row build.
func desiredSet(docs []Doc, projectOf map[string]string) (map[Key]string, map[Key]Doc) {
	hashes := make(map[Key]string, len(docs))
	byKey := make(map[Key]Doc, len(docs))
	for _, d := range docs {
		key := Key{Bank: BankFor(string(d.Kind), d.Slug), DocID: DocID(d.Slug, d.File)}
		hashes[key] = sourceHash(d)
		byKey[key] = d
		noteProject(projectOf, key.Bank, d.Slug)
	}
	return hashes, byKey
}

// actualSet reads every row of every cc- bank. A row without a document id was
// not written by sync and is left out of the diff. When onlySlug is set, rows
// of other projects are left out too, so a scoped run never plans their deletion.
// rowSlug receives the project slug stored on each row it returns.
func actualSet(mem *memory.Memory, projectOf map[string]string, rowSlug map[Key]string, onlySlug string) (map[Key]string, error) {
	rows, err := actualRows(mem, onlySlug)
	if err != nil {
		return nil, err
	}
	return splitRows(rows, projectOf, rowSlug), nil
}

// actualRows reads the rows of every cc- bank, keyed by bank and document id,
// under the same rules as actualSet. The rows are kept whole so an archive copy
// can be made of a row that is about to be deleted.
func actualRows(mem *memory.Memory, onlySlug string) (map[Key]memory.Entry, error) {
	banks, err := mem.Banks()
	if err != nil {
		return nil, err
	}
	out := map[Key]memory.Entry{}
	// Rows store the slug masked by memory.Redact, so the scope is matched in that form.
	scope := memory.Redact(onlySlug)
	for _, bank := range banks {
		if !strings.HasPrefix(bank, CentralBankPrefix) {
			continue
		}
		rows, _, err := mem.List(bank, 0, 0)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			id, _ := row.Metadata["document_id"].(string)
			if id == "" {
				continue
			}
			slug, _ := row.Metadata["project"].(string)
			if onlySlug != "" && slug != scope {
				continue
			}
			out[Key{Bank: bank, DocID: id}] = row
		}
	}
	return out, nil
}

// splitRows returns the source hash of each row, fills rowSlug with the project
// slug stored on each row and records the slug of every project bank in projectOf.
func splitRows(rows map[Key]memory.Entry, projectOf map[string]string, rowSlug map[Key]string) map[Key]string {
	actual := make(map[Key]string, len(rows))
	for key, row := range rows {
		slug, _ := row.Metadata["project"].(string)
		hash, _ := row.Metadata["source_hash"].(string)
		actual[key] = hash
		rowSlug[key] = slug
		noteProject(projectOf, key.Bank, slug)
	}
	return actual
}

// takeExcludedRows removes from actual the rows of excluded projects and returns
// their keys, sorted. Such a row is hard-deleted by the plan: DeleteDocuments
// rewrites the bank file and prunes the row's vectors from the index.
func takeExcludedRows(actual map[Key]string, rowSlug map[Key]string, ex excludeList) []Key {
	var keys []Key
	for key := range actual {
		if ex.excludes(rowSlug[key]) {
			keys = append(keys, key)
			delete(actual, key)
		}
	}
	sortKeys(keys)
	return keys
}

// excludedArchiveKeys returns the keys of the ArchiveBank rows of excluded
// projects, sorted. ArchiveBank is not a cc- bank, so the diff never sees them:
// they are hard-deleted here, with the same scope as the cc- rows.
func excludedArchiveKeys(mem *memory.Memory, onlySlug string, ex excludeList) ([]Key, error) {
	rows, _, err := mem.List(ArchiveBank, 0, 0)
	if err != nil {
		return nil, err
	}
	scope := memory.Redact(onlySlug)
	var keys []Key
	for _, row := range rows {
		slug := metaString(row, "project")
		if onlySlug != "" && slug != scope {
			continue
		}
		if ex.excludes(slug) {
			id, _ := row.Metadata["document_id"].(string)
			keys = append(keys, Key{Bank: ArchiveBank, DocID: id})
		}
	}
	sortKeys(keys)
	return keys, nil
}

// failedRows returns the keys of the rows that belong to a project whose scan
// failed. Rows store the slug masked by memory.Redact, so the comparison is made
// in that form, as the OnlySlug scope is.
func failedRows(rowSlug map[Key]string, failed []string) map[Key]bool {
	masked := make(map[string]bool, len(failed))
	for _, slug := range failed {
		masked[memory.Redact(slug)] = true
	}
	out := map[Key]bool{}
	for key, slug := range rowSlug {
		if masked[slug] {
			out[key] = true
		}
	}
	return out
}

// withholdFailed drops from plan.Deletes every row a run must keep. A row of a
// project whose scan failed is kept: the scan did not see its documents. Unless
// rebuild is set, a row of a project whose memory directory was not seen is kept
// too, because an absent directory is not a deletion. In both cases the plan
// reports deletes withheld.
func withholdFailed(plan Plan, rowSlug map[Key]string, disc Discovery, rebuild bool) Plan {
	held := failedRows(rowSlug, disc.FailedSlugs)
	if !rebuild {
		seen := make(map[string]bool, len(disc.Seen))
		for slug := range disc.Seen {
			seen[memory.Redact(slug)] = true
		}
		for key, slug := range rowSlug {
			if !seen[slug] {
				held[key] = true
			}
		}
	}
	kept := plan.Deletes[:0]
	for _, key := range plan.Deletes {
		if held[key] {
			plan.DeletesWithheld = true
			continue
		}
		kept = append(kept, key)
	}
	plan.Deletes = kept
	return plan
}

// noteProject records the project slug a project bank serves. The global bank
// serves no project, and the first slug seen for a bank wins.
func noteProject(projectOf map[string]string, bank, slug string) {
	if bank == GlobalBank || slug == "" {
		return
	}
	if _, ok := projectOf[bank]; !ok {
		projectOf[bank] = slug
	}
}

// sourceHash is the hash that decides whether a document's row is current: the
// render version, the deny-list digest and the file bytes.
func sourceHash(d Doc) string {
	sum := sha256.Sum256([]byte(renderVersion + "\x00" + memory.ScrubDigest() + "\x00" + d.FileSHA))
	return hex.EncodeToString(sum[:])
}

// rowFor maps one document to the row that holds it (plan 6.4).
func rowFor(d Doc, hash string) memory.Entry {
	id := DocID(d.Slug, d.File)
	links := d.Links
	if links == nil {
		links = []string{}
	}
	return memory.Entry{
		ID:        id,
		Content:   fmt.Sprintf("[%s] %s - %s\n%s", d.Kind, d.Name, d.Description, d.Body),
		Source:    claudeCodeSource,
		Timestamp: d.ModTime,
		Metadata: map[string]any{
			"document_id": id,
			"tags":        []string{"cc", "type:" + string(d.Kind)},
			"kind":        string(d.Kind),
			"name":        d.Name,
			"description": d.Description,
			"file":        d.File,
			"project":     d.Slug,
			"links":       links,
			"source_hash": hash,
		},
	}
}

// archiveKeys returns the deletes that keep a copy in ArchiveBank: rows whose
// document is no longer desired. A row whose document is still desired moved to
// another bank and is not archived. A forget archives nothing.
func archiveKeys(deletes []Key, desired map[Key]string, forget bool) []Key {
	if forget {
		return nil
	}
	docs := make(map[string]bool, len(desired))
	for key := range desired {
		docs[key.DocID] = true
	}
	var out []Key
	for _, key := range deletes {
		if !docs[key.DocID] {
			out = append(out, key)
		}
	}
	return out
}

// archivedRow is the copy of a deleted row that ArchiveBank keeps: the same
// content and metadata, stamped with the time sync deleted it. The ID is left
// empty so the bank assigns a fresh one and no archive copy shares a live row's ID.
func archivedRow(row memory.Entry, deletedAt string) memory.Entry {
	meta := make(map[string]any, len(row.Metadata)+1)
	maps.Copy(meta, row.Metadata)
	meta["deleted_at"] = deletedAt
	row.ID = ""
	row.Metadata = meta
	return row
}

// apply writes the plan one bank at a time: one RetainRaw of the archive copies
// of the bank's archived deletes, one RetainReplacing with the bank's upserts,
// then one DeleteDocuments with its deletes. A document being upserted is not
// deleted in the same bank, because RetainReplacing already drops its old row;
// deleting it afterwards would remove the new row too. An archive copy is written
// before its row is deleted, so a failed archive leaves the row in place.
func apply(ctx context.Context, mem *memory.Memory, plan Plan, hashes map[Key]string, docs map[Key]Doc, rows map[Key]memory.Entry) error {
	entries := map[string][]memory.Entry{}
	replace := map[string][]string{}
	upserting := make(map[Key]bool, len(plan.Upserts))
	for _, key := range plan.Upserts {
		entries[key.Bank] = append(entries[key.Bank], rowFor(docs[key], hashes[key]))
		replace[key.Bank] = append(replace[key.Bank], key.DocID)
		upserting[key] = true
	}
	archive := make(map[Key]bool, len(plan.Archives))
	for _, key := range plan.Archives {
		archive[key] = true
	}
	deletedAt := time.Now().UTC().Format(time.RFC3339)
	deletes := map[string][]string{}
	archives := map[string][]memory.Entry{}
	for _, key := range plan.Deletes {
		if upserting[key] {
			continue
		}
		deletes[key.Bank] = append(deletes[key.Bank], key.DocID)
		if archive[key] {
			archives[key.Bank] = append(archives[key.Bank], archivedRow(rows[key], deletedAt))
		}
	}

	banks := map[string]bool{}
	for bank := range entries {
		banks[bank] = true
	}
	for bank := range deletes {
		banks[bank] = true
	}
	names := make([]string, 0, len(banks))
	for bank := range banks {
		names = append(names, bank)
	}
	slices.Sort(names)

	for _, bank := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		bctx := context.WithoutCancel(ctx)
		if len(entries[bank]) > 0 {
			if _, err := mem.RetainReplacing(bctx, bank, entries[bank], replace[bank]); err != nil {
				return err
			}
		}
		if len(archives[bank]) > 0 {
			if err := mem.RetainRaw(bctx, ArchiveBank, archives[bank]); err != nil {
				return err
			}
		}
		if len(deletes[bank]) > 0 {
			if _, err := mem.DeleteDocuments(bctx, bank, deletes[bank]); err != nil {
				return err
			}
		}
	}
	return nil
}

// bankSummary reports every bank holding rows after the run, plus every bank
// the plan empties. Rows counts what each bank holds once the plan is applied.
func bankSummary(actual map[Key]string, plan Plan, projectOf map[string]string) []SyncBank {
	deleted := make(map[Key]bool, len(plan.Deletes))
	for _, key := range plan.Deletes {
		deleted[key] = true
	}
	after := map[Key]bool{}
	for key := range actual {
		if !deleted[key] {
			after[key] = true
		}
	}
	for _, key := range plan.Upserts {
		after[key] = true
	}
	rows := map[string]int{}
	for key := range after {
		rows[key.Bank]++
	}
	for _, key := range plan.Deletes {
		if _, ok := rows[key.Bank]; !ok && strings.HasPrefix(key.Bank, CentralBankPrefix) {
			rows[key.Bank] = 0
		}
	}
	names := make([]string, 0, len(rows))
	for bank := range rows {
		names = append(names, bank)
	}
	slices.Sort(names)
	out := make([]SyncBank, 0, len(names))
	for _, bank := range names {
		out = append(out, SyncBank{Bank: bank, Project: projectOf[bank], Rows: rows[bank]})
	}
	return out
}

// isDir reports whether path exists and is a directory.
func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
