package index

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/gitobj/gittest"
	"github.com/FreePeak/LeanKG/internal/langs"
	"github.com/FreePeak/LeanKG/internal/store"
)

// gitIndexStore opens a migrated store outside any repository under test.
func gitIndexStore(t *testing.T) store.Backend {
	t.Helper()
	st, err := store.OpenBackend(context.Background(), store.OpenOptions{DBPath: filepath.Join(t.TempDir(), "s.db"), Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st
}

// gitIndexRun runs one IndexDirWith pass over dir and fails the test on error.
func gitIndexRun(t *testing.T, st store.Backend, dir string) Result {
	t.Helper()
	res, err := IndexDirWith(context.Background(), st, dir, nil)
	if err != nil {
		t.Fatalf("IndexDirWith: %v", err)
	}
	return res
}

// gitIndexHas reports whether the store holds exactly one element with qn.
func gitIndexHas(t *testing.T, st store.Backend, qn string) bool {
	t.Helper()
	els, err := st.FindExact(context.Background(), qn)
	if err != nil {
		t.Fatal(err)
	}
	return len(els) == 1
}

// gitIndexFixture commits a.go, b.go, a .gitignore, and a nested repo recorded
// as a gitlink, then returns the root. Callers add the working-tree edits they
// need.
func gitIndexFixture(t *testing.T) string {
	t.Helper()
	gittest.SkipWithoutGit(t)
	root := t.TempDir()
	gittest.Git(t, root, "init", "-q")
	gittest.WriteFile(t, root, ".gitignore", "ignored.go\n")
	gittest.WriteFile(t, root, "a.go", "package a\n\nfunc Committed() {}\n")
	gittest.WriteFile(t, root, "b.go", "package b\n\nfunc Bee() {}\n")

	gittest.Git(t, root, "init", "-q", "nested")
	gittest.WriteFile(t, root, "nested/n.go", "package n\n\nfunc Nested() {}\n")
	gittest.Git(t, filepath.Join(root, "nested"), "add", "n.go")
	gittest.Git(t, filepath.Join(root, "nested"), "commit", "-q", "-m", "nested")

	gittest.Git(t, root, "add", ".gitignore", "a.go", "b.go", "nested")
	gittest.Git(t, root, "commit", "-q", "-m", "fixture")
	return root
}

// TestIndexGitRootIndexesHEADOnly: a repo root is indexed from HEAD, so
// uncommitted edits, untracked files, ignored files and gitlinked nested repos
// stay out of the store.
func TestIndexGitRootIndexesHEADOnly(t *testing.T) {
	root := gitIndexFixture(t)
	gittest.WriteFile(t, root, "a.go", "package a\n\nfunc Dirty() {}\n")
	gittest.WriteFile(t, root, "u.go", "package a\n\nfunc Untracked() {}\n")
	gittest.WriteFile(t, root, "ignored.go", "package a\n\nfunc Ignored() {}\n")
	st := gitIndexStore(t)

	gitIndexRun(t, st, root)

	if !gitIndexHas(t, st, "a.go::Committed") {
		t.Error("a.go::Committed missing: the committed content was not indexed")
	}
	for _, qn := range []string{"a.go::Dirty", "u.go::Untracked", "ignored.go::Ignored", "nested/n.go::Nested"} {
		if gitIndexHas(t, st, qn) {
			t.Errorf("%s indexed: the git root must index HEAD only", qn)
		}
	}
}

// TestIndexGitRootUnchangedRerunSkipsAll: a rerun over an unchanged HEAD skips
// every file by blob ID, even when the working copy of a committed file has
// been edited since the first run.
func TestIndexGitRootUnchangedRerunSkipsAll(t *testing.T) {
	root := gitIndexFixture(t)
	st := gitIndexStore(t)

	first := gitIndexRun(t, st, root)
	if first.Files != 2 {
		t.Fatalf("first run: Files = %d, want 2 (a.go, b.go)", first.Files)
	}

	gittest.WriteFile(t, root, "a.go", "package a\n\nfunc Dirty() {}\n")
	second := gitIndexRun(t, st, root)
	if second.Files != 0 || second.Skipped != first.Files {
		t.Fatalf("rerun: %+v, want Files=0 Skipped=%d", second, first.Files)
	}
	if !gitIndexHas(t, st, "a.go::Committed") {
		t.Error("a.go::Committed gone after rerun")
	}
}

// TestIndexGitRootNewCommitReextractsOnlyChanged: a new commit that edits one
// file re-extracts exactly that file. The other file has a dirty working copy
// that must not be read, because its blob ID is unchanged.
func TestIndexGitRootNewCommitReextractsOnlyChanged(t *testing.T) {
	root := gitIndexFixture(t)
	st := gitIndexStore(t)
	gitIndexRun(t, st, root)

	gittest.WriteFile(t, root, "a.go", "package a\n\nfunc Committed() {}\n\nfunc Added() {}\n")
	gittest.Git(t, root, "add", "a.go")
	gittest.Git(t, root, "commit", "-q", "-m", "edit a")
	gittest.WriteFile(t, root, "b.go", "package b\n\nfunc Bee() {}\n\nfunc Scratch() {}\n")

	res := gitIndexRun(t, st, root)
	if res.Files != 1 {
		t.Fatalf("after commit: Files = %d, want 1 (only a.go changed in HEAD)", res.Files)
	}
	if !gitIndexHas(t, st, "a.go::Added") {
		t.Error("a.go::Added missing after the commit")
	}
	if gitIndexHas(t, st, "b.go::Scratch") {
		t.Error("b.go::Scratch indexed: an unchanged blob must not be read from the working tree")
	}
}

// TestIndexGitRootRemovedInHEADIsDeleted: a file removed from HEAD is deleted
// from the store on the next run, and its elements go with it. The file stays
// on disk as untracked (git rm --cached), so only the HEAD source can see the
// removal.
func TestIndexGitRootRemovedInHEADIsDeleted(t *testing.T) {
	root := gitIndexFixture(t)
	st := gitIndexStore(t)
	gitIndexRun(t, st, root)
	if !gitIndexHas(t, st, "b.go::Bee") {
		t.Fatal("b.go::Bee not indexed on the first run")
	}

	gittest.Git(t, root, "rm", "-q", "--cached", "b.go")
	gittest.Git(t, root, "commit", "-q", "-m", "remove b")

	res := gitIndexRun(t, st, root)
	if res.DeletedFiles != 1 {
		t.Fatalf("DeletedFiles = %d, want 1", res.DeletedFiles)
	}
	if gitIndexHas(t, st, "b.go::Bee") {
		t.Error("b.go::Bee still indexed after b.go was removed from HEAD")
	}
}

// TestIndexGitRootAdoptsWalkStoreWithoutReextract: a store written by the walk
// (SHA-256 content hashes) adopts a git root that holds the same bytes without
// re-extracting: the content matches, so each record is re-keyed to its blob.
func TestIndexGitRootAdoptsWalkStoreWithoutReextract(t *testing.T) {
	gittest.SkipWithoutGit(t)
	dir := t.TempDir()
	gittest.WriteFile(t, dir, "a.go", "package a\n\nfunc A() {}\n")
	gittest.WriteFile(t, dir, "b.go", "package b\n\nfunc B() {}\n")
	st := gitIndexStore(t)

	if walked := gitIndexRun(t, st, dir); walked.Files != 2 {
		t.Fatalf("walk run: Files = %d, want 2", walked.Files)
	}

	gittest.Git(t, dir, "init", "-q")
	gittest.Git(t, dir, "add", "a.go", "b.go")
	gittest.Git(t, dir, "commit", "-q", "-m", "adopt")

	adopted := gitIndexRun(t, st, dir)
	if adopted.Files != 0 {
		t.Fatalf("adopting run: Files = %d, want 0 (identical bytes)", adopted.Files)
	}
	recs, err := st.Files(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("file records = %d, want 2", len(recs))
	}
	for _, rec := range recs {
		if !strings.HasPrefix(rec.ContentHash, "git:") {
			t.Errorf("%s ContentHash = %q, want a git: blob key", rec.Path, rec.ContentHash)
		}
	}
	if again := gitIndexRun(t, st, dir); again.Files != 0 || again.Skipped != 2 {
		t.Fatalf("third run: %+v, want Files=0 Skipped=2", again)
	}
}

// TestIndexUnbornRepoUsesWalk: a repository with no commit has no HEAD tree, so
// the indexer falls back to the filesystem walk.
func TestIndexUnbornRepoUsesWalk(t *testing.T) {
	gittest.SkipWithoutGit(t)
	dir := t.TempDir()
	gittest.Git(t, dir, "init", "-q")
	gittest.WriteFile(t, dir, "a.go", "package a\n\nfunc Working() {}\n")
	gittest.WriteFile(t, dir, "b.go", "package b\n\nfunc Other() {}\n")
	st := gitIndexStore(t)

	res := gitIndexRun(t, st, dir)
	if res.Files != 2 {
		t.Fatalf("Files = %d, want 2 from the working tree", res.Files)
	}
	if !gitIndexHas(t, st, "a.go::Working") {
		t.Error("a.go::Working missing: an unborn repo must be indexed from disk")
	}
}

// TestIndexRepoSubdirUsesWalk: indexing a subdirectory of a repository is not a
// repo root, so it reads the working tree, including uncommitted edits.
func TestIndexRepoSubdirUsesWalk(t *testing.T) {
	root := gitIndexFixture(t)
	gittest.WriteFile(t, root, "sub/s.go", "package s\n\nfunc Sub() {}\n")
	gittest.Git(t, root, "add", "sub/s.go")
	gittest.Git(t, root, "commit", "-q", "-m", "sub")
	gittest.WriteFile(t, root, "sub/s.go", "package s\n\nfunc SubEdited() {}\n")
	st := gitIndexStore(t)

	gitIndexRun(t, st, filepath.Join(root, "sub"))
	if !gitIndexHas(t, st, "s.go::SubEdited") {
		t.Error("s.go::SubEdited missing: a subdirectory of a repo must be walked from disk")
	}
}

// TestSupportedFilesGitRootListsHEAD: doctor's disk side of the freshness check
// lists what the git indexer takes, so the untracked u.go is not a supported
// file of a repository root with a commit.
func TestSupportedFilesGitRootListsHEAD(t *testing.T) {
	gittest.SkipWithoutGit(t)
	root := t.TempDir()
	gittest.Git(t, root, "init", "-q")
	gittest.WriteFile(t, root, "a.go", "package a\n")
	gittest.Git(t, root, "add", "a.go")
	gittest.Git(t, root, "commit", "-q", "-m", "init")
	gittest.WriteFile(t, root, "u.go", "package u\n")

	reg := langs.NewRegistry()
	if _, err := reg.Activate(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	files, err := SupportedFiles(root, reg)
	if err != nil {
		t.Fatalf("SupportedFiles: %v", err)
	}
	if want := []string{"a.go"}; !slices.Equal(files, want) {
		t.Fatalf("SupportedFiles = %q, want %q (u.go is untracked)", files, want)
	}
}

// crossFileSources is the package crossFileFixture commits. a.go, the file the
// existing test edits, calls into b.go and c.go, so the only cross-file edges
// leave it. b.go's betaHelper calls alphaTwo, which b.go defines itself, so it
// adds no cross-file edge. No unchanged file calls into a.go, so an incoming
// edge into the edited file is not covered by this fixture.
var crossFileSources = map[string]string{
	"a.go": "package pkg\n\n// Alpha drives the cross-file calls.\nfunc Alpha() {\n\tbetaHelper()\n\tgammaHelper()\n}\n\n// AlphaTwo calls into c.go.\nfunc AlphaTwo() {\n\tgammaHelper()\n}\n",
	"b.go": "package pkg\n\n// betaHelper calls back into a.go.\nfunc betaHelper() {\n\talphaTwo()\n}\n\nfunc alphaTwo() {}\n",
	"c.go": "package pkg\n\nfunc gammaHelper() {}\n",
}

// crossFileAlphaReworded is a.go after a comment-only edit.
const crossFileAlphaReworded = "package pkg\n\n// Alpha drives the cross-file calls, reworded.\nfunc Alpha() {\n\tbetaHelper()\n\tgammaHelper()\n}\n\n// AlphaTwo calls into c.go.\nfunc AlphaTwo() {\n\tgammaHelper()\n}\n"

// deltaSiblingSources has c.go calling deltaHelper, which no file defines yet.
var deltaSiblingSources = map[string]string{
	"a.go": "package pkg\n\nfunc Alpha() {}\n",
	"b.go": "package pkg\n\nfunc Beta() {}\n",
	"c.go": "package pkg\n\nfunc gammaHelper() {\n\tdeltaHelper()\n}\n",
}

// deltaAlphaWithHelper is a.go after the commit that defines deltaHelper.
const deltaAlphaWithHelper = "package pkg\n\nfunc Alpha() {}\n\nfunc deltaHelper() {}\n"

// writeSources writes files (name -> source) under root.
func writeSources(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, src := range files {
		gittest.WriteFile(t, root, name, src)
	}
}

// commitAll stages every change under root and commits it.
func commitAll(t *testing.T, root, msg string) {
	t.Helper()
	gittest.Git(t, root, "add", "-A")
	gittest.Git(t, root, "commit", "-q", "-m", msg)
}

// gitPackageFixture commits files (name -> source) as one git root and returns it.
func gitPackageFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	gittest.SkipWithoutGit(t)
	root := t.TempDir()
	gittest.Git(t, root, "init", "-q")
	writeSources(t, root, files)
	commitAll(t, root, "fixture")
	return root
}

// crossFileFixture commits crossFileSources as one git root.
func crossFileFixture(t *testing.T) string {
	t.Helper()
	return gitPackageFixture(t, crossFileSources)
}

// crossFileEdges returns the store's relationships whose endpoints sit in
// different files, keyed as "source -> target [rel_type]".
func crossFileEdges(t *testing.T, st store.Backend) map[string]bool {
	t.Helper()
	rels, err := st.RelationshipsAll(context.Background(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, r := range rels {
		srcFile, _, _ := strings.Cut(r.Source, "::")
		dstFile, _, _ := strings.Cut(r.Target, "::")
		if srcFile != dstFile {
			out[r.Source+" -> "+r.Target+" ["+r.RelType+"]"] = true
		}
	}
	return out
}

// allEdges returns every relationship in st keyed as
// "source -> target [rel_type] confidence".
func allEdges(t *testing.T, st store.Backend) map[string]bool {
	t.Helper()
	rels, err := st.RelationshipsAll(context.Background(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, r := range rels {
		out[fmt.Sprintf("%s -> %s [%s] %g", r.Source, r.Target, r.RelType, r.Confidence)] = true
	}
	return out
}

// reportEdgeDiff fails the test, naming each edge present on one side only,
// when the got and want edge sets differ.
func reportEdgeDiff(t *testing.T, label string, got, want map[string]bool) {
	t.Helper()
	var missing, extra []string
	for e := range want {
		if !got[e] {
			missing = append(missing, e)
		}
	}
	for e := range got {
		if !want[e] {
			extra = append(extra, e)
		}
	}
	if len(missing) == 0 && len(extra) == 0 {
		return
	}
	slices.Sort(missing)
	slices.Sort(extra)
	t.Errorf("%s\n  missing (%d): %q\n  extra (%d): %q", label, len(missing), missing, len(extra), extra)
}

// assertEdgesMatchFresh indexes root into a fresh store and fails unless st holds
// the same relationships (source, target, type, confidence).
func assertEdgesMatchFresh(t *testing.T, st store.Backend, root string) {
	t.Helper()
	fresh := gitIndexStore(t)
	gitIndexRun(t, fresh, root)
	want := allEdges(t, fresh)
	if len(want) == 0 {
		t.Fatal("fresh index holds no relationships: the fixture exercises nothing")
	}
	reportEdgeDiff(t, "incremental edges differ from a fresh full index", allEdges(t, st), want)
}

// gitIndexIncoming returns the relationships whose target is target.
func gitIndexIncoming(t *testing.T, st store.Backend, target string) []store.Relationship {
	t.Helper()
	rels, err := st.Incoming(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	return rels
}

// gitIndexHasEdge reports whether st holds source -> target with relType.
func gitIndexHasEdge(t *testing.T, st store.Backend, source, target, relType string) bool {
	t.Helper()
	for _, r := range gitIndexIncoming(t, st, target) {
		if r.Source == source && r.RelType == relType {
			return true
		}
	}
	return false
}

// TestIndexGitRootIncrementalKeepsCrossFileEdges: after a commit edits one file,
// the incremental run must hold the same cross-file edges as a fresh full index
// of that commit.
func TestIndexGitRootIncrementalKeepsCrossFileEdges(t *testing.T) {
	root := crossFileFixture(t)
	st := gitIndexStore(t)
	gitIndexRun(t, st, root)

	gittest.WriteFile(t, root, "a.go", crossFileAlphaReworded)
	gittest.Git(t, root, "add", "a.go")
	gittest.Git(t, root, "commit", "-q", "-m", "edit a")
	gitIndexRun(t, st, root)

	fresh := gitIndexStore(t)
	gitIndexRun(t, fresh, root)
	reportEdgeDiff(t, "incremental cross-file edges differ from a fresh full index", crossFileEdges(t, st), crossFileEdges(t, fresh))
	assertEdgesMatchFresh(t, st, root)
}

// TestIndexGitRootIncrementalLinksSiblingToNewSymbol: c.go calls deltaHelper,
// which no file defines at first. The commit adds it to a.go, so the unchanged
// c.go must gain an edge to the new symbol.
func TestIndexGitRootIncrementalLinksSiblingToNewSymbol(t *testing.T) {
	root := gitPackageFixture(t, deltaSiblingSources)
	st := gitIndexStore(t)
	gitIndexRun(t, st, root)

	gittest.WriteFile(t, root, "a.go", deltaAlphaWithHelper)
	commitAll(t, root, "add deltaHelper")

	res := gitIndexRun(t, st, root)
	if res.Files != 1 || res.Skipped != 2 {
		t.Fatalf("incremental run: %+v, want Files=1 Skipped=2", res)
	}
	if !gitIndexHasEdge(t, st, "c.go::gammaHelper", "a.go::deltaHelper", "calls") {
		t.Error("missing edge c.go::gammaHelper -> a.go::deltaHelper [calls]: the unchanged sibling was not linked to the new symbol")
	}
	assertEdgesMatchFresh(t, st, root)
}

// TestIndexGitRootIncrementalFollowsMovedSymbol: movedHelper moves from b.go to
// a.go in one commit. The unchanged c.go calls it, so its edge must follow the
// symbol to a.go, and no edge may keep pointing at b.go.
func TestIndexGitRootIncrementalFollowsMovedSymbol(t *testing.T) {
	root := gitPackageFixture(t, map[string]string{
		"a.go": "package pkg\n\nfunc Alpha() {}\n",
		"b.go": "package pkg\n\nfunc movedHelper() {}\n",
		"c.go": "package pkg\n\nfunc gammaHelper() {\n\tmovedHelper()\n}\n",
	})
	st := gitIndexStore(t)
	gitIndexRun(t, st, root)

	gittest.WriteFile(t, root, "a.go", "package pkg\n\nfunc Alpha() {}\n\nfunc movedHelper() {}\n")
	gittest.WriteFile(t, root, "b.go", "package pkg\n\nfunc Beta() {}\n")
	commitAll(t, root, "move movedHelper to a.go")

	res := gitIndexRun(t, st, root)
	if res.Files != 2 || res.Skipped != 1 {
		t.Fatalf("incremental run: %+v, want Files=2 Skipped=1", res)
	}
	if !gitIndexHasEdge(t, st, "c.go::gammaHelper", "a.go::movedHelper", "calls") {
		t.Error("missing edge c.go::gammaHelper -> a.go::movedHelper [calls]: the sibling edge did not follow the moved symbol")
	}
	if got := gitIndexIncoming(t, st, "b.go::movedHelper"); len(got) != 0 {
		t.Errorf("%d edges still target the removed b.go::movedHelper: %+v", len(got), got)
	}
	assertEdgesMatchFresh(t, st, root)
}

// TestIndexGitRootIncrementalDropsEdgesToRemovedSymbol: doomedHelper is removed
// from a.go while the unchanged c.go still calls it. No maintenance run is
// performed, so the incremental pass itself must drop the dangling edge.
func TestIndexGitRootIncrementalDropsEdgesToRemovedSymbol(t *testing.T) {
	root := gitPackageFixture(t, map[string]string{
		"a.go": "package pkg\n\nfunc Alpha() {\n\tBeta()\n}\n\nfunc doomedHelper() {}\n",
		"b.go": "package pkg\n\nfunc Beta() {}\n",
		"c.go": "package pkg\n\nfunc gammaHelper() {\n\tdoomedHelper()\n}\n",
	})
	st := gitIndexStore(t)
	gitIndexRun(t, st, root)

	// Alpha -> Beta survives the commit, so the fresh index is not empty.
	gittest.WriteFile(t, root, "a.go", "package pkg\n\nfunc Alpha() {\n\tBeta()\n}\n")
	commitAll(t, root, "remove doomedHelper")
	gitIndexRun(t, st, root)

	if got := gitIndexIncoming(t, st, "a.go::doomedHelper"); len(got) != 0 {
		t.Errorf("%d edges still target the removed a.go::doomedHelper: %+v", len(got), got)
	}
	assertEdgesMatchFresh(t, st, root)
}

// countingStore records which files an index run upserts elements for and deletes.
type countingStore struct {
	store.Backend
	upserted map[string]int
	deleted  map[string]int
}

func (s *countingStore) UpsertElements(ctx context.Context, els []store.Element) error {
	for _, e := range els {
		s.upserted[e.FilePath]++
	}
	return s.Backend.UpsertElements(ctx, els)
}

func (s *countingStore) DeleteByFile(ctx context.Context, path string) error {
	s.deleted[path]++
	return s.Backend.DeleteByFile(ctx, path)
}

// siblingSnapshot is the state an incremental run must leave alone for unchanged
// files: their element rows, file-record content hashes and embedding states,
// plus the model's vector count.
type siblingSnapshot struct {
	elements map[string]store.Element
	hashes   map[string]string
	states   map[string]string
	vectors  int
}

func takeSiblingSnapshot(t *testing.T, st store.Backend, model string, paths ...string) siblingSnapshot {
	t.Helper()
	ctx := context.Background()
	els, err := st.Elements(ctx)
	if err != nil {
		t.Fatal(err)
	}
	files, err := st.Files(ctx)
	if err != nil {
		t.Fatal(err)
	}
	states, err := st.EmbeddingStateMap(ctx, model)
	if err != nil {
		t.Fatal(err)
	}
	vectors, err := st.VectorCount(ctx, model)
	if err != nil {
		t.Fatal(err)
	}
	snap := siblingSnapshot{elements: map[string]store.Element{}, hashes: map[string]string{}, states: map[string]string{}, vectors: vectors}
	for _, e := range els {
		if slices.Contains(paths, e.FilePath) {
			snap.elements[e.QualifiedName] = e
		}
	}
	for _, f := range files {
		if slices.Contains(paths, f.Path) {
			snap.hashes[f.Path] = f.ContentHash
		}
	}
	for qn, state := range states {
		file, _, _ := strings.Cut(qn, "::")
		if slices.Contains(paths, file) {
			snap.states[qn] = state
		}
	}
	return snap
}

// nearestVector returns the qualified name of the vector closest to vec.
func nearestVector(t *testing.T, st store.Backend, model string, vec []float32) string {
	t.Helper()
	hits, err := st.SearchVectors(context.Background(), store.VectorQuery{ModelID: model, Vec: vec, K: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("SearchVectors: %d hits, want 1", len(hits))
	}
	return hits[0].Element.QualifiedName
}

// TestIndexGitRootIncrementalKeepsSiblingVectors: an incremental run that
// rewrites a.go must leave the embedding state, vectors, element rows and file
// records of the unchanged siblings b.go and c.go as they were, and must upsert
// and delete only a.go.
func TestIndexGitRootIncrementalKeepsSiblingVectors(t *testing.T) {
	const model = "test-model"
	root := gitPackageFixture(t, deltaSiblingSources)
	st := gitIndexStore(t)
	gitIndexRun(t, st, root)

	ctx := context.Background()
	if err := st.SetEmbeddingStates(ctx, model, map[string]string{"b.go::Beta": "hash-beta", "c.go::gammaHelper": "hash-gamma"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertVectors(ctx, model, []store.VectorRow{
		{QualifiedName: "b.go::Beta", Vec: []float32{0, 0, 1}},
		{QualifiedName: "c.go::gammaHelper", Vec: []float32{0, 1, 0}},
	}); err != nil {
		t.Fatal(err)
	}
	before := takeSiblingSnapshot(t, st, model, "b.go", "c.go")
	if got := nearestVector(t, st, model, []float32{0, 0, 1}); got != "b.go::Beta" {
		t.Fatalf("before the run: nearest vector = %q, want b.go::Beta", got)
	}

	gittest.WriteFile(t, root, "a.go", deltaAlphaWithHelper)
	commitAll(t, root, "add deltaHelper")
	cs := &countingStore{Backend: st, upserted: map[string]int{}, deleted: map[string]int{}}
	gitIndexRun(t, cs, root)

	after := takeSiblingSnapshot(t, st, model, "b.go", "c.go")
	if !reflect.DeepEqual(before.elements, after.elements) {
		t.Errorf("sibling element rows changed:\n  before %+v\n  after  %+v", before.elements, after.elements)
	}
	if !reflect.DeepEqual(before.hashes, after.hashes) {
		t.Errorf("sibling file ContentHash changed: before %v, after %v", before.hashes, after.hashes)
	}
	if !reflect.DeepEqual(before.states, after.states) {
		t.Errorf("sibling embedding state changed: before %v, after %v", before.states, after.states)
	}
	if before.vectors != after.vectors {
		t.Errorf("VectorCount: before %d, after %d", before.vectors, after.vectors)
	}
	if got := nearestVector(t, st, model, []float32{0, 0, 1}); got != "b.go::Beta" {
		t.Errorf("after the run: nearest vector = %q, want the sibling b.go::Beta", got)
	}
	for path := range cs.upserted {
		if path != "a.go" {
			t.Errorf("UpsertElements called for %s, want only a.go", path)
		}
	}
	for path := range cs.deleted {
		if path != "a.go" {
			t.Errorf("DeleteByFile called for %s, want only a.go", path)
		}
	}
	if cs.upserted["a.go"] == 0 {
		t.Error("UpsertElements never called for a.go: the counting store saw no run")
	}
}

// TestIndexWalkIncrementalKeepsCrossFileEdges: the cross-file scenario of the git
// test, run through the walk path of a plain directory with no git repository.
func TestIndexWalkIncrementalKeepsCrossFileEdges(t *testing.T) {
	root := t.TempDir()
	writeSources(t, root, crossFileSources)
	st := gitIndexStore(t)
	gitIndexRun(t, st, root)

	gittest.WriteFile(t, root, "a.go", crossFileAlphaReworded)
	gitIndexRun(t, st, root)

	assertEdgesMatchFresh(t, st, root)
}
