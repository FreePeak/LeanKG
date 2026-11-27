package ccmemory

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/FreePeak/LeanKG/internal/sessionlink/claudecode"
)

// GlobalBank holds every user and feedback memory from every repository.
const GlobalBank = "cc-global"

// ProjectKey returns the slug of the repository that owns dir's memory.
// Candidates in order: the slug of the main worktree root of dir, then the
// slug of dir. The first with a memory directory under projectsRoot wins.
// With neither present, the main-root slug is used when a main root was
// found, else the slug of dir. No git subprocess runs.
func ProjectKey(projectsRoot, dir string) string {
	own := claudecode.Slug(dir)
	root, ok := mainRoot(dir)
	if !ok {
		return own
	}
	main := claudecode.Slug(root)
	if hasMemoryDir(projectsRoot, main) {
		return main
	}
	if hasMemoryDir(projectsRoot, own) {
		return own
	}
	return main
}

func hasMemoryDir(projectsRoot, slug string) bool {
	fi, err := os.Stat(filepath.Join(projectsRoot, slug, "memory"))
	return err == nil && fi.IsDir()
}

// mainRoot walks up from dir to the nearest .git. A directory means its
// parent is the main root. A file is a gitdir pointer: its target's commondir
// names the shared .git, whose parent is the main root.
func mainRoot(dir string) (string, bool) {
	d := filepath.Clean(dir)
	for {
		gitPath := filepath.Join(d, ".git")
		if fi, err := os.Stat(gitPath); err == nil {
			if fi.IsDir() {
				return d, true
			}
			return rootFromPointer(d, gitPath)
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", false
		}
		d = parent
	}
}

// rootFromPointer reads a .git file ("gitdir: <path>") in worktree and returns
// the main root named by the commondir of that gitdir.
func rootFromPointer(worktree, gitFile string) (string, bool) {
	raw, err := os.ReadFile(gitFile)
	if err != nil {
		return "", false
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir:")
	if !ok {
		return "", false
	}
	gitdir = resolveFrom(worktree, strings.TrimSpace(gitdir))
	common, err := os.ReadFile(filepath.Join(gitdir, "commondir"))
	if err != nil {
		return "", false
	}
	shared := resolveFrom(gitdir, strings.TrimSpace(string(common)))
	return filepath.Dir(shared), true
}

// resolveFrom joins p onto base unless p is already absolute.
func resolveFrom(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(base, p))
}

// ProjectBank is the bank for one repository's project, reference and untyped
// memories. A hash keeps long slugs with a shared prefix apart after the
// 64-character bank cut.
func ProjectBank(slug string) string {
	return "cc-p-" + shortHex(slug, 16)
}

// BankFor picks the bank for a memory of the given raw kind string. Kinds
// user and feedback are global; everything else is repository-scoped.
func BankFor(kind, slug string) string {
	switch kind {
	case "user", "feedback":
		return GlobalBank
	}
	return ProjectBank(slug)
}

// DocID is the document and row id for one memory file of one repository.
// It is hash-based so redaction cannot rewrite it (see KD-4).
func DocID(slug, file string) string {
	return "ccm-" + shortHex(slug+"\x00"+file, 24)
}

// shortHex returns the first n hex characters of sha256(s).
func shortHex(s string, n int) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:n]
}
