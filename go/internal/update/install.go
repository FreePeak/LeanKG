package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// binaryNames are the members release-go.yml packs at the archive root —
// `tar -czf leankg-<goos>-<goarch>.tgz -C out leankg leankg-embed`.
var binaryNames = []string{"leankg", "leankg-embed"}

// backupSuffix names the previous binary alongside the new one.
const backupSuffix = ".old"

// maxMember bounds one unpacked tar member.
//
// ponytail: 128 MiB is a fixed ceiling calibrated to the real binaries (`leankg`
// ≈ 33 MB, `leankg-embed` ≈ 21 MB), not an adaptive limit; the upgrade path is
// sizing it from the release asset's declared `size`. It exists to bound a
// zip-bomb-style archive, not to police an honest release.
const maxMember = 128 << 20

// untarGzip reads a downloaded .tgz and returns member name → bytes for the two
// release binaries. Every rejection below is a refusal to trust an archive that
// is not exactly what the workflow packs: a member that is not a regular file
// (symlink, directory, device, hardlink — a tarball that links outside itself is
// an attack, not an archive), a name outside the two binaries (which covers `..`,
// absolute paths and subdirectories), a member over maxMember, and a duplicate
// member (an overwrite in disguise) are all errors.
//
// Extraction is pure: it reads the archive into memory and touches no install
// path, so a failure here provably leaves the working install untouched.
func untarGzip(data []byte) (map[string][]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("the release archive is not valid gzip (download truncated or corrupt): %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	out := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("the release archive is corrupt: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("the release archive contains %q (%s) — expected only regular files %q and %q",
				hdr.Name, typeflagName(hdr.Typeflag), binaryNames[0], binaryNames[1])
		}
		if hdr.Name != filepath.Clean(hdr.Name) || !allowedMember(hdr.Name) {
			return nil, fmt.Errorf("the release archive contains unexpected member %q", hdr.Name)
		}
		if _, dup := out[hdr.Name]; dup {
			return nil, fmt.Errorf("the release archive contains %q twice", hdr.Name)
		}
		if hdr.Size < 0 || hdr.Size > maxMember {
			return nil, fmt.Errorf("the release archive member %q declares %d bytes", hdr.Name, hdr.Size)
		}
		buf := make([]byte, hdr.Size)
		if _, err := io.ReadFull(tr, buf); err != nil {
			return nil, fmt.Errorf("the release archive is corrupt: reading %s: %w", hdr.Name, err)
		}
		out[hdr.Name] = buf
	}
	for _, name := range binaryNames {
		if _, ok := out[name]; !ok {
			return nil, fmt.Errorf("the release archive is missing the %s binary", name)
		}
	}
	return out, nil
}

// allowedMember accepts exactly the bare names the workflow packs. The check runs
// on the raw header name after the caller proved Clean(name) == name, so a
// `./leankg` or `out/leankg` entry is rejected rather than normalised into
// acceptance: a release whose layout changed must fail loudly, not install a
// file the workflow no longer builds.
func allowedMember(name string) bool {
	for _, n := range binaryNames {
		if name == n {
			return true
		}
	}
	return false
}

func typeflagName(b byte) string {
	switch b {
	case tar.TypeDir:
		return "directory"
	case tar.TypeSymlink:
		return "symlink"
	case tar.TypeLink:
		return "hardlink"
	default:
		return fmt.Sprintf("type %c", b)
	}
}

// verifySum checks the bytes that arrived against the checksum the release
// published. Callers run it before unpacking, so a mismatch costs nothing beyond
// the download. An empty want means none was published (see Run).
func verifySum(got, want string) error {
	if want == "" || strings.EqualFold(got, want) {
		return nil
	}
	return fmt.Errorf("checksum mismatch: the release published %s, the download hashed to %s", want, got)
}

// verifyMembers is the evidence a release provides when it ships no checksum for
// the asset (release-go.yml uploads only the .tgz): the archive must hold both
// binaries and each must be stamped with the requested version. It returns the
// notice the caller shows the user, which says out loud what was verified and
// what was not.
//
// ponytail: the stamp check is a substring scan of the binary, so it can
// false-positive on an unrelated embedded string of the same shape (the ceiling).
// The upgrade path is publishing a checksum: once release-go.yml also uploads a
// `.sha256` asset (or the API records a digest), the checksum branch verifies the
// whole archive and this scan is moot.
func verifyMembers(members map[string][]byte, wantVersion string) (string, error) {
	for _, name := range binaryNames {
		data, ok := members[name]
		if !ok || len(data) == 0 {
			return "", fmt.Errorf("the release archive is missing the %s binary", name)
		}
		if !bytes.Contains(data, []byte(wantVersion)) {
			return "", fmt.Errorf("the %s binary in the release archive is not stamped with version %s — refusing to install", name, wantVersion)
		}
	}
	return fmt.Sprintf("This release publishes no checksum, so the archive was verified instead: both binaries (%s) are present and stamped with version %s.",
		strings.Join(binaryNames, ", "), wantVersion), nil
}

// sumHex is the lowercase hex SHA256 of data.
func sumHex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// installBinary replaces target with data and returns the backup path (empty when
// there was no previous file to back up).
//
// The sequence is write-sibling-then-rename, never delete-then-copy: the payload
// lands in a temp file inside target's own directory (the same filesystem as the
// running binary, so the swap cannot fail with EXDEV), is fsynced and chmod'ed
// 0755, the previous file is copied aside to target+backupSuffix, and only then
// does one os.Rename move the new file into place. A running process keeps
// executing the old inode across the rename; if the rename fails, the backup is
// renamed back before the error is returned, so the directory is never left
// without its binary.
func installBinary(target string, data []byte) (backup string, err error) {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".leankg-update-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	removeTmp := func() { os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		removeTmp()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		removeTmp()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		removeTmp()
		return "", err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil { // CreateTemp hands back 0600
		removeTmp()
		return "", err
	}
	bk := target + backupSuffix
	if err := copyFile(target, bk); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			removeTmp()
			return "", err
		}
		bk = "" // fresh install: nothing to back up, nothing to restore
	}
	if err := os.Rename(tmpName, target); err != nil {
		if bk != "" {
			os.Rename(bk, target) // best-effort: the old binary is back in place
		}
		removeTmp()
		return "", err
	}
	// A failed directory fsync can only mean the swap is not yet durable; the
	// worst case after a crash is the previous binary, which is safe, so it is not
	// worth failing an update that visibly succeeded.
	syncDir(dir)
	return bk, nil
}

// copyFile copies src to dst with mode 0755, fsynced.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// syncDir fsyncs a directory so a completed rename survives a crash.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// PermError reports a permission failure at the install location. Command is the
// exact re-run to copy-paste (e.g. `sudo /usr/local/bin/leankg update`). Unwrap
// yields the underlying error, so errors.Is(err, fs.ErrPermission) and
// errors.As(err, &pe) both work at the call site.
type PermError struct {
	Dir     string
	Command string
	Err     error
}

func (e *PermError) Error() string {
	return fmt.Sprintf("no write permission for %s (the directory holding the running binary). Re-run as:\n  %s", e.Dir, e.Command)
}

func (e *PermError) Unwrap() error { return e.Err }
