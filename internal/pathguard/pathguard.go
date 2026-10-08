// Package pathguard confines file arguments to a project root (RS-01).
//
// Every tool argument that names a file (import read, query compress, query
// lsp document, import docs/prd/ontology) goes through Resolve. The check is
// made by os.Root, which refuses `..` climbs, absolute paths and symlinks
// that leave the root at the OS level instead of by string comparison — the
// class of bug a HasPrefix check reintroduces through symlinks.
//
// ceiling: Resolve returns a path, and callers open it afterwards, so a
// symlink swapped inside the project between the check and the open is not
// caught. The threat being closed is a client naming a file outside the
// project; a writer already inside the project tree is out of scope.
package pathguard

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/FreePeak/LeanKG/internal/errs"
)

// ErrNotFound reports a path that is inside the root but does not exist.
var ErrNotFound = errors.New("pathguard: not found")

// Resolve maps p to an absolute path inside root. A relative p is taken
// relative to root; an absolute p must already lie under root (or under its
// symlink-resolved form). The returned rel is root-relative with forward
// slashes, for messages that must not leak the server's filesystem layout.
func Resolve(root, p string) (abs, rel string, err error) {
	if strings.TrimSpace(p) == "" {
		return "", "", fmt.Errorf("pathguard: empty path")
	}
	if root == "" {
		var werr error
		if root, werr = os.Getwd(); werr != nil {
			return "", "", werr
		}
	}
	realRoot := root
	if r, rerr := filepath.EvalSymlinks(root); rerr == nil {
		realRoot = r
	}
	rel, ok := relativeTo(p, root, realRoot)
	if !ok {
		return "", "", outside(p)
	}
	r, err := os.OpenRoot(realRoot)
	if err != nil {
		return "", "", err
	}
	defer r.Close()
	if _, serr := r.Stat(rel); serr != nil {
		if errors.Is(serr, fs.ErrNotExist) {
			return "", "", fmt.Errorf("%w: %s", ErrNotFound, filepath.ToSlash(rel))
		}
		// os.Root refuses every symlink with an ABSOLUTE target, including one
		// pointing back inside the tree. Accept those only when the fully
		// resolved target still lies under the resolved root.
		if target, eerr := filepath.EvalSymlinks(filepath.Join(realRoot, rel)); eerr == nil && within(target, realRoot) {
			return target, filepath.ToSlash(rel), nil
		}
		return "", "", outside(p)
	}
	return filepath.Join(realRoot, rel), filepath.ToSlash(rel), nil
}

func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// relativeTo expresses p relative to root. Absolute paths qualify only when
// they sit under root or its resolved form; relative paths pass through for
// os.Root to judge (it rejects any climb out).
func relativeTo(p, root, realRoot string) (string, bool) {
	if !filepath.IsAbs(p) {
		return filepath.Clean(p), true
	}
	clean := filepath.Clean(p)
	for _, base := range []string{root, realRoot} {
		if rel, err := filepath.Rel(base, clean); err == nil &&
			rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return rel, true
		}
	}
	return "", false
}

func outside(p string) error {
	return errs.NewError(errs.PathOutsideProject,
		fmt.Sprintf("%q resolves outside the project directory", p), "")
}

// IsOutside reports whether err is a confinement refusal.
func IsOutside(err error) bool {
	var e *errs.Error
	return errors.As(err, &e) && e.Code() == errs.PathOutsideProject.Code
}
