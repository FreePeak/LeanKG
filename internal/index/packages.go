package index

import (
	"context"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/FreePeak/LeanKG/internal/gitobj"
	"github.com/FreePeak/LeanKG/internal/store"
)

// Go package elements. Every directory with a Go source file (test files
// included) is one element of type "package": qualified name and file_path are
// the module-relative directory, so ReplaceFileRelationships(dir, ...) owns its
// imports edges. Its imports edges name the other package directories its files
// import, resolved by crossIndex.dirForImport (longest suffix of the import
// path). Stdlib and external imports resolve to no directory and make no edge.
//
// The union of a directory's imports is kept in a KV record (pkgImportsKV), so
// an incremental run can re-resolve an unchanged importer when a package appears
// without re-reading its files.
const (
	packageElementType = "package"
	importsRelType     = "imports"
	// pkgImportsKV holds, per package directory, its import union (encodePkgRecord).
	pkgImportsKV = "go-pkg-imports"
	pkgRecord    = "pkg"
)

// pkgImports maps each import path a package names to test_only: true when
// every importing file is a _test.go file.
type pkgImports map[string]bool

// goImports is the import union of a directory's Go files.
func goImports(fes []fileElements) pkgImports {
	out := pkgImports{}
	for _, fe := range fes {
		testOnly := isTestFile(fe.rel)
		for _, p := range fe.imports {
			if prev, seen := out[p]; seen {
				out[p] = prev && testOnly
			} else {
				out[p] = testOnly
			}
		}
	}
	return out
}

// inTestdata reports whether a module-relative directory is, or sits below, a
// testdata directory. Go tooling does not build testdata as packages either.
func inTestdata(dir string) bool {
	return slices.Contains(strings.Split(dir, "/"), "testdata")
}

// goPackageDirs are the directories that hold a Go candidate, except testdata.
func goPackageDirs(cands []candidate) map[string]bool {
	dirs := map[string]bool{}
	for _, c := range cands {
		if c.lang != "go" {
			continue
		}
		d := dirOf(c.rel)
		if !inTestdata(d) {
			dirs[d] = true
		}
	}
	return dirs
}

// packageResolver resolves import paths to the package directories of a run.
type packageResolver struct {
	cx   *crossIndex
	pkgs map[string]bool
}

func newPackageResolver(pkgs map[string]bool) packageResolver {
	cx := &crossIndex{base: map[string][]string{}}
	for d := range pkgs {
		if d != "." {
			cx.base[baseOf(d)] = append(cx.base[baseOf(d)], d)
		}
	}
	return packageResolver{cx: cx, pkgs: pkgs}
}

// edges is the imports edges of package dir: one per package directory the
// import union resolves to, never to dir itself. test_only is the AND over the
// import paths that resolve to the same directory.
func (r packageResolver) edges(dir string, imps pkgImports) []store.Relationship {
	testOnly := map[string]bool{}
	for imp, t := range imps {
		target := r.cx.dirForImport(imp)
		if target == "" || target == dir || !r.pkgs[target] {
			continue
		}
		if prev, seen := testOnly[target]; seen {
			testOnly[target] = prev && t
		} else {
			testOnly[target] = t
		}
	}
	var out []store.Relationship
	for _, target := range slices.Sorted(maps.Keys(testOnly)) {
		rel := store.Relationship{Source: dir, Target: target, RelType: importsRelType, Confidence: 1.0}
		if testOnly[target] {
			rel.Metadata = map[string]any{"test_only": true}
		}
		out = append(out, rel)
	}
	return out
}

// encodePkgRecord writes the import union as a marker line, then one
// "path<TAB>t|n" line per import (t: test_only), sorted.
func encodePkgRecord(imps pkgImports) string {
	lines := []string{pkgRecord}
	for _, imp := range slices.Sorted(maps.Keys(imps)) {
		flag := "n"
		if imps[imp] {
			flag = "t"
		}
		lines = append(lines, imp+"\t"+flag)
	}
	return strings.Join(lines, "\n")
}

// decodePkgRecord reads a record back; ok is false when the directory has none.
func decodePkgRecord(v string) (imps pkgImports, ok bool) {
	lines := strings.Split(v, "\n")
	if len(lines) == 0 || lines[0] != pkgRecord {
		return nil, false
	}
	imps = pkgImports{}
	for _, line := range lines[1:] {
		imp, flag, found := strings.Cut(line, "\t")
		if found {
			imps[imp] = flag == "t"
		}
	}
	return imps, true
}

// refreshPackages is the package pass of a run, before the directory writes. It
//   - removes the package of a directory that lost its last Go file (element,
//     edges, record); the orphan sweep removes the edges that name it;
//   - re-reads every Go file of a directory that lost one but keeps some, and of
//     a package with no record yet, and writes it;
//   - re-resolves the imports edges of unchanged importers of a package created
//     in this run, which the run would otherwise leave without the edge.
//
// Changed directories are written by writeGoPackage from the directory loop. It
// returns the resolver the loop uses. goGone lists the directory of every Go file
// this run deleted; changed is the set of directories with a changed file.
func refreshPackages(ctx context.Context, st Store, cands []candidate, goGone []string, changed map[string]bool, rd *gitobj.Reader, res *Result) (packageResolver, error) {
	if len(changed) == 0 && len(goGone) == 0 {
		return packageResolver{}, nil
	}
	pkgs := goPackageDirs(cands)
	r := newPackageResolver(pkgs)
	goCands := map[string][]candidate{}
	for _, c := range cands {
		if d := dirOf(c.rel); c.lang == "go" && pkgs[d] {
			goCands[d] = append(goCands[d], c)
		}
	}

	// Records of every package: absent means the package is new to the store.
	recs := map[string]pkgImports{}
	isNew := map[string]bool{}
	for d := range pkgs {
		v, _, err := st.KVGet(ctx, pkgImportsKV, d)
		if err != nil {
			return r, err
		}
		if imps, ok := decodePkgRecord(v); ok {
			recs[d] = imps
		} else {
			isNew[d] = true
		}
	}

	removed := map[string]bool{}
	for _, d := range goGone {
		if pkgs[d] || removed[d] {
			continue
		}
		removed[d] = true
		if err := st.DeleteByFile(ctx, d); err != nil {
			return r, err
		}
		if err := st.KVSet(ctx, pkgImportsKV, d, ""); err != nil {
			return r, err
		}
	}

	// Packages to re-read: lost a Go file, or new and not changed by this run.
	refresh := map[string]bool{}
	for _, d := range goGone {
		if pkgs[d] && !changed[d] {
			refresh[d] = true
		}
	}
	for d := range isNew {
		if !changed[d] {
			refresh[d] = true
		}
	}
	for _, d := range slices.Sorted(maps.Keys(refresh)) {
		fes, err := extractSiblings(ctx, goCands[d], nil, rd)
		if err != nil {
			return r, err
		}
		if err := writeGoPackage(ctx, st, r, d, goImports(fes), res); err != nil {
			return r, err
		}
	}

	// Unchanged importers of a package created in this run.
	if len(isNew) > 0 {
		for _, d := range slices.Sorted(maps.Keys(recs)) {
			if changed[d] || refresh[d] {
				continue
			}
			if !importsAny(r, recs[d], isNew) {
				continue
			}
			if err := st.ReplaceFileRelationships(context.WithoutCancel(ctx), d, []string{importsRelType}, r.edges(d, recs[d])); err != nil {
				return r, err
			}
		}
	}
	return r, nil
}

// importsAny reports whether one of imps resolves to a directory in set.
func importsAny(r packageResolver, imps pkgImports, set map[string]bool) bool {
	for imp := range imps {
		if set[r.cx.dirForImport(imp)] {
			return true
		}
	}
	return false
}

// writeGoPackage stores package dir: its element, its imports edges and its record.
// The record is written last, so a run cut short before it leaves the directory
// without one, and the rerun writes it again.
func writeGoPackage(ctx context.Context, st Store, r packageResolver, dir string, imps pkgImports, res *Result) error {
	wctx := context.WithoutCancel(ctx)
	el := store.Element{
		QualifiedName: dir, ElementType: packageElementType, Name: path.Base(dir),
		FilePath: dir, Language: "go",
	}
	if err := st.UpsertElements(wctx, []store.Element{el}); err != nil {
		return err
	}
	rels := r.edges(dir, imps)
	if err := st.ReplaceFileRelationships(wctx, dir, []string{importsRelType}, rels); err != nil {
		return err
	}
	if err := st.KVSet(wctx, pkgImportsKV, dir, encodePkgRecord(imps)); err != nil {
		return err
	}
	res.Elements++
	res.Relationships += len(rels)
	return nil
}
