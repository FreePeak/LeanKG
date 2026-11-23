package ab_test

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// pinnedCorpus copies the repository tree at rev into a temp dir and returns
// that dir with the resolved commit id. It uses `git archive`, so the copy holds
// the tracked files of that commit only: uncommitted and untracked files, and
// edits to tracked files, never reach the index or the truth generator.
// Symlinks and submodule entries are skipped.
func pinnedCorpus(t testing.TB, rev string) (dir, commit string) {
	t.Helper()
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	commit = strings.TrimSpace(gitOutput(t, repo, "rev-parse", rev+"^{commit}"))
	dir = t.TempDir()

	cmd := exec.Command("git", "-C", repo, "archive", "--format=tar", commit)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(out)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read git archive: %v", err)
		}
		target := filepath.Join(dir, filepath.FromSlash(hdr.Name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(target, 0o755)
		case tar.TypeReg:
			err = writeArchiveFile(target, hdr.Mode, tr)
		}
		if err != nil {
			t.Fatalf("extract %s: %v", hdr.Name, err)
		}
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("git archive %s: %v: %s", commit, err, stderr.String())
	}
	return dir, commit
}

func writeArchiveFile(target string, mode int64, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(mode&0o777)|0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func gitOutput(t testing.TB, repo string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return string(out)
}
