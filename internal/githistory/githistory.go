// Package githistory records a repository's commit history in the store as
// ordinary elements and relationships, so commits are searchable and the file
// graph carries what changed together. It reads git through internal/gitobj
// and writes only through store.Backend.
//
// Shape (see docs/git-history.md):
//   - commit element  git:commit:<sha>     element_type commit
//   - author element  git:author:<email>   element_type author
//   - file element    git:file:<path>      element_type file (file_path empty,
//     so re-indexing a source file never deletes its history node)
//   - file --modified_in--> commit, commit --authored_by--> author,
//     file --co_changed_with--> file (count in metadata, both directions)
package githistory

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/FreePeak/LeanKG/internal/gitobj"
	"github.com/FreePeak/LeanKG/internal/store"
)

const (
	// kvNamespace and kvHeadKey hold the last imported HEAD. The history
	// cursor is its own fact, separate from the index's recorded head.
	kvNamespace = "githistory"
	kvHeadKey   = "head"

	// DefaultMaxCommits bounds the walk of a full import.
	DefaultMaxCommits = 5000
	// maxCoChangeFiles is the largest commit that contributes co-change pairs.
	// A commit touching more files is a mass edit (format, rename, vendor
	// drop) and would add quadratic noise.
	maxCoChangeFiles = 50
	// maxContentBytes bounds a commit element's content.
	maxContentBytes = 8000
	// maxListedFiles bounds the per-file listing inside a commit's content.
	maxListedFiles = 50
)

// Options bounds an import. MaxCommits <= 0 means DefaultMaxCommits.
type Options struct {
	MaxCommits int
}

// Result reports what one import wrote.
type Result struct {
	Head          string `json:"head"`
	Commits       int    `json:"commits"`
	Files         int    `json:"files"`
	CoChangePairs int    `json:"co_change_pairs"`
	Incremental   bool   `json:"incremental"`
}

// Import records the history of the repository at root into st. When the
// last imported HEAD is still an ancestor of HEAD, only the new commits are
// walked and co-change counts are added to the stored ones. Otherwise the
// window is rebuilt and co-change counts are overwritten, so a rebuild never
// double counts.
func Import(ctx context.Context, st store.Backend, root string, opts Options) (Result, error) {
	head, ok, err := gitobj.HeadCommit(ctx, root)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		return Result{}, nil
	}
	stored, _, err := st.KVGet(kvNamespace, kvHeadKey)
	if err != nil {
		return Result{}, err
	}
	if stored == head {
		return Result{Head: head, Incremental: true}, nil
	}
	walk := gitobj.HistoryOptions{Max: opts.MaxCommits}
	if walk.Max <= 0 {
		walk.Max = DefaultMaxCommits
	}
	incremental := false
	if stored != "" {
		incremental, err = gitobj.IsAncestor(ctx, root, stored)
		if err != nil {
			return Result{}, err
		}
	}
	if incremental {
		walk.Since = stored
	}
	commits, err := gitobj.History(ctx, root, walk)
	if err != nil {
		return Result{}, err
	}
	b := newBatch()
	for _, c := range commits {
		b.add(c)
	}
	pairs, err := b.coChange(st, incremental)
	if err != nil {
		return Result{}, err
	}
	if err := st.UpsertElements(b.elements()); err != nil {
		return Result{}, err
	}
	if err := st.UpsertRelationships(b.relationships(pairs)); err != nil {
		return Result{}, err
	}
	if err := st.KVSet(kvNamespace, kvHeadKey, head); err != nil {
		return Result{}, err
	}
	return Result{
		Head:          head,
		Commits:       len(commits),
		Files:         len(b.files),
		CoChangePairs: len(pairs),
		Incremental:   incremental,
	}, nil
}

// pairKey is a co-change pair in canonical order (a < b).
type pairKey struct{ a, b string }

func newPair(x, y string) pairKey {
	if x > y {
		x, y = y, x
	}
	return pairKey{x, y}
}

// batch collects the rows one import writes.
type batch struct {
	commits []gitobj.HistoryCommit
	authors map[string]store.Element
	files   map[string]bool
	delta   map[pairKey]int
}

func newBatch() *batch {
	return &batch{authors: map[string]store.Element{}, files: map[string]bool{}, delta: map[pairKey]int{}}
}

func (b *batch) add(c gitobj.HistoryCommit) {
	b.commits = append(b.commits, c)
	b.authors[c.AuthorEmail] = authorElement(c)
	paths := make([]string, 0, len(c.Files))
	for _, f := range c.Files {
		b.files[f.Path] = true
		paths = append(paths, f.Path)
	}
	if len(paths) <= maxCoChangeFiles {
		for i := range paths {
			for j := i + 1; j < len(paths); j++ {
				if paths[i] != paths[j] {
					b.delta[newPair(paths[i], paths[j])]++
				}
			}
		}
	}
}

// coChange returns the final count of every pair in this batch. An
// incremental import adds the batch to the counts already stored on the
// edges; a rebuild keeps the batch counts as they are.
func (b *batch) coChange(st store.Backend, incremental bool) (map[pairKey]int, error) {
	out := make(map[pairKey]int, len(b.delta))
	for k, n := range b.delta {
		out[k] = n
	}
	if !incremental {
		return out, nil
	}
	// Each stored pair is written once from its canonical side (a), so one
	// read per touched file covers every pair it anchors.
	outgoing := map[string][]store.Relationship{}
	for k := range b.delta {
		rels, ok := outgoing[k.a]
		if !ok {
			var err error
			if rels, err = st.Outgoing("git:file:" + k.a); err != nil {
				return nil, err
			}
			outgoing[k.a] = rels
		}
		for _, r := range rels {
			if r.RelType == "co_changed_with" && r.Target == "git:file:"+k.b {
				if n, ok := r.Metadata["count"].(float64); ok {
					out[k] += int(n)
				}
			}
		}
	}
	return out, nil
}

func (b *batch) elements() []store.Element {
	els := make([]store.Element, 0, len(b.commits)+len(b.authors)+len(b.files))
	for _, c := range b.commits {
		els = append(els, commitElement(c))
	}
	for _, a := range b.authors {
		els = append(els, a)
	}
	paths := make([]string, 0, len(b.files))
	for p := range b.files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		els = append(els, store.Element{
			QualifiedName: "git:file:" + p,
			ElementType:   "file",
			Name:          p,
		})
	}
	return els
}

func (b *batch) relationships(pairs map[pairKey]int) []store.Relationship {
	var rels []store.Relationship
	for _, c := range b.commits {
		sha := "git:commit:" + c.SHA
		rels = append(rels, store.Relationship{
			Source: sha, Target: "git:author:" + c.AuthorEmail, RelType: "authored_by", Confidence: 1,
		})
		for _, f := range c.Files {
			rels = append(rels, store.Relationship{
				Source: "git:file:" + f.Path, Target: sha, RelType: "modified_in", Confidence: 1,
				Metadata: map[string]any{
					"sha": c.SHA, "date": c.Date, "author": c.AuthorName,
					"subject": c.Subject, "insertions": f.Insertions, "deletions": f.Deletions,
				},
			})
		}
	}
	for k, n := range pairs {
		meta := map[string]any{"count": n}
		rels = append(rels,
			store.Relationship{Source: "git:file:" + k.a, Target: "git:file:" + k.b, RelType: "co_changed_with", Confidence: 1, Metadata: meta},
			store.Relationship{Source: "git:file:" + k.b, Target: "git:file:" + k.a, RelType: "co_changed_with", Confidence: 1, Metadata: meta},
		)
	}
	return rels
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func commitElement(c gitobj.HistoryCommit) store.Element {
	insertions, deletions := 0, 0
	var listing strings.Builder
	for i, f := range c.Files {
		insertions += f.Insertions
		deletions += f.Deletions
		if i < maxListedFiles {
			fmt.Fprintf(&listing, "%s (+%d -%d)\n", f.Path, f.Insertions, f.Deletions)
		}
	}
	var content strings.Builder
	content.WriteString(c.Subject)
	if c.Body != "" {
		content.WriteString("\n\n" + c.Body)
	}
	fmt.Fprintf(&content, "\n\nAuthor: %s <%s>\nDate: %s\nChanged %d files (+%d -%d)\n%s",
		c.AuthorName, c.AuthorEmail, c.Date, len(c.Files), insertions, deletions, listing.String())
	parents := c.Parents
	if parents == nil {
		parents = []string{}
	}
	return store.Element{
		QualifiedName: "git:commit:" + c.SHA,
		ElementType:   "commit",
		Name:          shortSHA(c.SHA) + " " + c.Subject,
		Content:       store.ClipUTF8(content.String(), maxContentBytes),
		Metadata: map[string]any{
			"sha": c.SHA, "author": c.AuthorName, "email": c.AuthorEmail, "date": c.Date,
			"parents": parents, "files_changed": len(c.Files),
			"insertions": insertions, "deletions": deletions,
		},
	}
}

func authorElement(c gitobj.HistoryCommit) store.Element {
	return store.Element{
		QualifiedName: "git:author:" + c.AuthorEmail,
		ElementType:   "author",
		Name:          c.AuthorName,
		Content:       c.AuthorName + " <" + c.AuthorEmail + ">",
	}
}
