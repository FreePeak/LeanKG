package index

import (
	"context"

	"github.com/FreePeak/LeanKG/internal/gitobj"
	"github.com/FreePeak/LeanKG/internal/langs"
)

// SupportedFiles walks dir with the same discovery rules as the indexer
// (skipDirs + dot-directory skip, extension gate through the registry's
// lazy activation, 1 MiB size cap) and returns the project-root-relative
// slash paths of exactly the files `leankg index` would take. doctor
// --deep uses it as the disk side of the index-freshness comparison, so
// the two must never drift — this shares the indexer's own tables. A
// repository root with a commit lists HEAD's files, as the indexer reads them;
// any other directory is walked on disk.
func SupportedFiles(dir string, reg *langs.Registry) ([]string, error) {
	if reg == nil {
		reg = langs.DefaultRegistry()
	}
	owner := func(ext string) (string, bool) {
		l, ok := reg.ExtOwner(ext)
		return string(l), ok
	}
	blobs, fromHEAD, err := gitobj.HeadBlobs(context.Background(), dir)
	if err != nil {
		return nil, err
	}
	var skipped Result // the size-cap count is the indexer's; this lists paths only
	var cands []candidate
	if fromHEAD {
		cands = gitCandidates(blobs, owner, &skipped)
	} else if cands, err = walkCandidates(context.Background(), dir, owner, &skipped); err != nil {
		return nil, err
	}
	var out []string
	for _, c := range cands {
		out = append(out, c.rel)
	}
	return out, nil
}
