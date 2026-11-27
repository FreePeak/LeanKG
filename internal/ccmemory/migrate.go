package ccmemory

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/FreePeak/LeanKG/internal/memory"
)

// ProjectInventory is one project slug as the migrate inventory reports it.
// Files counts the slug's documents, Global the ones that land in cc-global
// and Skipped the slug's files that discovery did not read.
type ProjectInventory struct {
	Slug    string `json:"slug"`
	Bank    string `json:"bank"`
	Files   int    `json:"files"`
	Global  int    `json:"global"`
	Skipped int    `json:"skipped"`
}

// BuildInventory groups a discovery by project slug, sorted by slug.
func BuildInventory(disc Discovery, projectsRoot string) []ProjectInventory {
	bySlug := map[string]*ProjectInventory{}
	entry := func(slug string) *ProjectInventory {
		p, ok := bySlug[slug]
		if !ok {
			p = &ProjectInventory{Slug: slug, Bank: ProjectBank(slug)}
			bySlug[slug] = p
		}
		return p
	}
	for _, d := range disc.Docs {
		p := entry(d.Slug)
		p.Files++
		if BankFor(string(d.Kind), d.Slug) == GlobalBank {
			p.Global++
		}
	}
	for _, s := range disc.Skipped {
		entry(slugOf(projectsRoot, s.Path)).Skipped++
	}
	out := make([]ProjectInventory, 0, len(bySlug))
	for _, p := range bySlug {
		out = append(out, *p)
	}
	slices.SortFunc(out, func(a, b ProjectInventory) int {
		return strings.Compare(a.Slug, b.Slug)
	})
	return out
}

// Mismatch is one row that differs from the desired set after an apply.
type Mismatch struct {
	Bank   string `json:"bank"`
	DocID  string `json:"document_id"`
	Reason string `json:"reason"` // "missing", "extra" or "hash"
}

const (
	mismatchMissing = "missing"
	mismatchExtra   = "extra"
	mismatchHash    = "hash"
)

// VerifyRows compares the rows read back after an apply with the desired set.
// It reports each missing row, each extra row and each row whose hash differs,
// sorted by bank, then document id. An equal pair returns an empty, non-nil slice.
func VerifyRows(desired, actual map[Key]string) []Mismatch {
	out := []Mismatch{}
	for key, want := range desired {
		have, ok := actual[key]
		switch {
		case !ok:
			out = append(out, Mismatch{Bank: key.Bank, DocID: key.DocID, Reason: mismatchMissing})
		case have != want:
			out = append(out, Mismatch{Bank: key.Bank, DocID: key.DocID, Reason: mismatchHash})
		}
	}
	for key := range actual {
		if _, ok := desired[key]; !ok {
			out = append(out, Mismatch{Bank: key.Bank, DocID: key.DocID, Reason: mismatchExtra})
		}
	}
	slices.SortFunc(out, func(a, b Mismatch) int {
		return cmp.Or(strings.Compare(a.Bank, b.Bank), strings.Compare(a.DocID, b.DocID), strings.Compare(a.Reason, b.Reason))
	})
	return out
}

// MigrateResult is the outcome of one migrate run, in the JSON shape of plan
// 6.4a. Deleted and DeletesWithheld are for the human summary only.
type MigrateResult struct {
	Root            string             `json:"root"`
	Projects        []ProjectInventory `json:"projects"`
	Documents       int                `json:"documents"`
	Upserted        int                `json:"upserted"`
	Skipped         int                `json:"skipped"`
	Verified        bool               `json:"verified"`
	Mismatches      []Mismatch         `json:"mismatches"`
	Deleted         int                `json:"-"`
	DeletesWithheld bool               `json:"-"`
}

// Migrate is the one-time import of Claude Code memory into the central root.
// It runs the sync apply for opts (limited to opts.OnlySlug when set) and then,
// still under the sync lock, reads the cc- rows back and compares them with the
// desired set. A dry run plans only: nothing is applied and nothing is verified.
func Migrate(ctx context.Context, mem *memory.Memory, opts SyncOptions) (MigrateResult, error) {
	if err := requireSlugMemory(mem, opts); err != nil {
		return MigrateResult{}, err
	}
	var (
		verified   bool
		mismatches = []Mismatch{}
	)
	verify := func(st syncState) error {
		actual, err := actualSet(mem, st.projectOf, opts.OnlySlug)
		if err != nil {
			return err
		}
		mismatches = VerifyRows(st.desired, actual)
		verified = len(mismatches) == 0
		return nil
	}
	res, st, err := syncPass(ctx, mem, opts, verify)
	if err != nil {
		return MigrateResult{}, err
	}
	return MigrateResult{
		Root:            res.Root,
		Projects:        BuildInventory(st.disc, opts.Projects),
		Documents:       res.Documents,
		Upserted:        res.Upserted,
		Skipped:         res.Skipped,
		Verified:        verified,
		Mismatches:      mismatches,
		Deleted:         res.Deleted,
		DeletesWithheld: res.DeletesWithheld,
	}, nil
}

// requireSlugMemory fails a scoped migrate whose slug has neither a memory
// directory under the projects root nor any row in the central banks: the slug
// is mistyped. It runs before the sync lock, so the error writes nothing. A slug
// with central rows but no source keeps sync's withheld-deletes behaviour.
func requireSlugMemory(mem *memory.Memory, opts SyncOptions) error {
	if opts.OnlySlug == "" || isDir(memoryDirOf(opts.Projects, opts.OnlySlug)) {
		return nil
	}
	rows, err := actualSet(mem, map[string]string{}, opts.OnlySlug)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return fmt.Errorf("no memory found for project %s", opts.OnlySlug)
	}
	return nil
}
