package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// fake serves one GitHub release and its tarball from an httptest server, so Run
// exercises the real HTTP, tar, checksum and rename paths with no network and no
// real $HOME. Fields are set before Run is called.
type fake struct {
	srv       *httptest.Server
	tag       string
	body      string
	digest    string // the API `digest` field of the served asset
	tgz       []byte
	apiStatus int // non-zero overrides the release endpoint's status
	downloads atomic.Int32
}

// newFake serves release `tag` with a leankg-linux-amd64.tgz asset that packs
// `members` exactly as release.yml does (bare names at the archive root).
func newFake(t *testing.T, tag string, members map[string][]byte) *fake {
	t.Helper()
	f := &fake{tag: tag, tgz: makeTGZ(t, members)}
	mux := http.NewServeMux()
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	mux.HandleFunc("/repos/"+DefaultRepo+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if f.apiStatus != 0 {
			w.WriteHeader(f.apiStatus)
			fmt.Fprint(w, `{"message":"API rate limit exceeded for IP."}`)
			return
		}
		json.NewEncoder(w).Encode(Release{
			TagName: f.tag,
			Name:    "leankg " + strings.TrimPrefix(f.tag, "v"),
			Body:    f.body,
			Assets:  []Asset{f.asset()},
		})
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) {
		f.downloads.Add(1)
		w.Write(f.tgz)
	})
	return f
}

// asset is the linux/amd64 tarball this fake serves.
func (f *fake) asset() Asset {
	name := assetName("linux", "amd64")
	return Asset{
		Name:               name,
		BrowserDownloadURL: f.srv.URL + "/dl/" + name,
		Digest:             f.digest,
		Size:               int64(len(f.tgz)),
	}
}

// publishChecksum models a release that records the SHA256 of the archive it
// uploaded — GitHub's asset digest, which release.yml's body table also feeds.
func (f *fake) publishChecksum() { f.digest = "sha256:" + sumHex(f.tgz) }

// opts is the Run input for this fake: an install dir in a temp tree holding both
// binaries at `current`, pinned to linux/amd64.
func (f *fake) opts(t *testing.T, current string) (Options, string) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range binaryNames {
		if err := os.WriteFile(filepath.Join(dir, name), stampedBin(current), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return Options{
		CurrentVersion: current,
		ExePath:        filepath.Join(dir, "leankg"),
		GOOS:           "linux",
		GOARCH:         "amd64",
		APIBase:        f.srv.URL,
		Client:         f.srv.Client(),
	}, dir
}

// makeTGZ packs members the way the release workflow does: bare names, mode 0755,
// at the archive root.
func makeTGZ(t *testing.T, members map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, data := range members {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Typeflag: tar.TypeReg, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// stampedBin is a fake release binary carrying a version the way the real ones do:
// `-X main.version=<ver>` plus the //go:embed VERSION copy.
func stampedBin(ver string) []byte {
	return []byte("\x7fELF fake binary main.version=" + ver + "\x00VERSION=" + ver + "\x00")
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return data
}

// entries lists an install directory's contents, so a test can prove a run leaves
// nothing stray behind (no temp files, no probe files).
func entries(t *testing.T, dir string) []string {
	t.Helper()
	des, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(des))
	for _, de := range des {
		out = append(out, de.Name())
	}
	return out
}

func mustRun(t *testing.T, o Options) Result {
	t.Helper()
	res, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	return res
}

// assertInstallUntouched is the rollback contract: original bytes, no backups, no
// strays.
func assertInstallUntouched(t *testing.T, dir, wantVersion string) {
	t.Helper()
	for _, name := range binaryNames {
		if got := mustRead(t, filepath.Join(dir, name)); !bytes.Contains(got, []byte(wantVersion)) {
			t.Fatalf("%s was modified (%q); want the original %s build", name, got, wantVersion)
		}
		if _, err := os.Stat(filepath.Join(dir, name+backupSuffix)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s%s appeared although the update failed", name, backupSuffix)
		}
	}
	if got := entries(t, dir); len(got) != 2 {
		t.Fatalf("install dir = %v; a failed run must leave exactly the original 2 files", got)
	}
}

func TestLatestResolvesReleaseAndAsset(t *testing.T) {
	f := newFake(t, "v0.32.0", map[string][]byte{
		"leankg":       stampedBin("0.32.0"),
		"leankg-embed": stampedBin("0.32.0"),
	})
	rel, err := Latest(context.Background(), f.srv.Client(), f.srv.URL, DefaultRepo, "")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if rel.Version() != "0.32.0" {
		t.Fatalf("Version() = %q; the v-prefix belongs to the tag, not the version", rel.Version())
	}
	if _, ok := rel.AssetFor("linux", "amd64"); !ok {
		t.Fatalf("AssetFor(linux, amd64) found nothing in %+v", rel.Assets)
	}
	if _, ok := rel.AssetFor("windows", "amd64"); ok {
		t.Fatal("AssetFor matched windows/amd64; the release matrix builds none")
	}
}

func TestLatestReportsRateLimitAndMissingRelease(t *testing.T) {
	f := newFake(t, "v0.32.0", nil)
	for _, tc := range []struct {
		status int
		want   string
	}{
		{http.StatusForbidden, "rate-limited"},
		{http.StatusTooManyRequests, "GITHUB_TOKEN"},
		{http.StatusNotFound, "no published release"},
		{http.StatusInternalServerError, "HTTP 500"},
	} {
		f.apiStatus = tc.status
		_, err := Latest(context.Background(), f.srv.Client(), f.srv.URL, DefaultRepo, "")
		if err == nil {
			t.Fatalf("HTTP %d must be an error", tc.status)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("HTTP %d error = %q, want it to say %q", tc.status, err, tc.want)
		}
	}
}

// TestChecksumResolution pins the resolution order and the name binding: the API
// digest wins, a body checksum counts only on the line naming our asset, and
// anything else means no checksum was published.
func TestChecksumResolution(t *testing.T) {
	ours := Asset{Name: "leankg-linux-amd64.tgz"}
	linuxSum := strings.Repeat("b", 64)
	darwinSum := strings.Repeat("d", 64)
	rel := Release{TagName: "v1.0.0", Assets: []Asset{ours}}
	if got := checksum(rel, ours); got != "" {
		t.Fatalf("checksum with nothing published = %q, want empty", got)
	}

	body := Release{TagName: "v1.0.0", Assets: []Asset{ours}, Body: strings.Join([]string{
		"sha256  " + darwinSum + "  leankg-darwin-arm64.tgz", // a sibling platform
		"sha256  " + linuxSum + "  " + ours.Name,             // ours
	}, "\n")}
	if got := checksum(body, ours); got != linuxSum {
		t.Fatalf("body checksum = %q, want %q", got, linuxSum)
	}
	// A body that only names a sibling platform must yield nothing: applying the
	// wrong platform's digest would reject a good download (or accept a bad one).
	sibling := Release{TagName: "v1.0.0", Assets: []Asset{ours}, Body: "sha256  " + linuxSum + "  leankg-darwin-arm64.tgz"}
	if got := checksum(sibling, ours); got != "" {
		t.Fatalf("checksum taken from a sibling platform's line: %q", got)
	}

	// The API digest outranks the body.
	withDigest := Release{TagName: "v1.0.0", Body: body.Body, Assets: []Asset{{Name: ours.Name, Digest: "sha256:" + strings.Repeat("e", 64)}}}
	if got := checksum(withDigest, withDigest.Assets[0]); got != strings.Repeat("e", 64) {
		t.Fatalf("digest checksum = %q", got)
	}
	// A digest over another algorithm verifies nothing here.
	sha1 := Release{TagName: "v1.0.0", Assets: []Asset{{Name: ours.Name, Digest: "sha1:" + strings.Repeat("e", 40)}}}
	if got := checksum(sha1, sha1.Assets[0]); got != "" {
		t.Fatalf("a sha1 digest must verify nothing, got %q", got)
	}
}

func TestRunUpToDateDoesNotDownload(t *testing.T) {
	f := newFake(t, "v0.31.0", map[string][]byte{
		"leankg":       stampedBin("0.31.0"),
		"leankg-embed": stampedBin("0.31.0"),
	})
	o, dir := f.opts(t, "0.31.0")
	res := mustRun(t, o)
	if res.Status != StatusUpToDate {
		t.Fatalf("Status = %q, want %q", res.Status, StatusUpToDate)
	}
	if res.Current != "0.31.0" || res.Latest != "0.31.0" {
		t.Fatalf("versions = %q / %q", res.Current, res.Latest)
	}
	if n := f.downloads.Load(); n != 0 {
		t.Fatalf("an up-to-date run downloaded %d times; equal versions must not fetch", n)
	}
	assertInstallUntouched(t, dir, "0.31.0")
}

// TestRunNewerThanLatestCoversTheDevBuildCase: a local build ahead of the newest
// release must say so instead of downgrading the user.
func TestRunNewerThanLatestCoversTheDevBuildCase(t *testing.T) {
	f := newFake(t, "v0.30.0", map[string][]byte{
		"leankg":       stampedBin("0.30.0"),
		"leankg-embed": stampedBin("0.30.0"),
	})
	o, dir := f.opts(t, "0.31.0")
	res := mustRun(t, o)
	if res.Status != StatusUpToDate {
		t.Fatalf("Status = %q, want up-to-date (never downgrade)", res.Status)
	}
	if !strings.Contains(res.Notice, "newer") {
		t.Fatalf("Notice = %q, want an explanation that the running build is newer", res.Notice)
	}
	assertInstallUntouched(t, dir, "0.31.0")
}

func TestRunAtomicReplaceWithChecksum(t *testing.T) {
	f := newFake(t, "v0.32.0", map[string][]byte{
		"leankg":       stampedBin("0.32.0"),
		"leankg-embed": stampedBin("0.32.0"),
	})
	f.publishChecksum()
	o, dir := f.opts(t, "0.31.0")
	res := mustRun(t, o)

	if res.Status != StatusUpdated || !res.ChecksumVerified || res.Notice != "" {
		t.Fatalf("status=%q verified=%v notice=%q; a published checksum must be the evidence",
			res.Status, res.ChecksumVerified, res.Notice)
	}
	if res.AssetName != "leankg-linux-amd64.tgz" {
		t.Fatalf("AssetName = %q", res.AssetName)
	}
	for _, name := range binaryNames {
		if got := mustRead(t, filepath.Join(dir, name)); !bytes.Contains(got, []byte("0.32.0")) {
			t.Fatalf("%s still holds the old build: %q", name, got)
		}
		bk, err := os.Stat(filepath.Join(dir, name+backupSuffix))
		if err != nil {
			t.Fatalf("%s: the previous binary was not backed up: %v", name, err)
		}
		if bk.Mode().Perm()&0o100 == 0 {
			t.Fatalf("%s%s is not executable; a backup of a binary stays runnable", name, backupSuffix)
		}
	}
	if fi, err := os.Stat(filepath.Join(dir, "leankg")); err != nil || fi.Mode().Perm()&0o111 == 0 {
		t.Fatalf("the swapped binary is not executable: %v", err)
	}
	if b := mustRead(t, filepath.Join(dir, "leankg"+backupSuffix)); !bytes.Contains(b, []byte("0.31.0")) {
		t.Fatalf("leankg.old does not hold the previous build: %q", b)
	}
	// Exactly the two binaries plus their two backups: no temp or probe leftovers.
	if got := entries(t, dir); len(got) != 4 {
		t.Fatalf("install dir = %v, want the 2 binaries and their 2 backups", got)
	}
	if len(res.Replaced) != 2 || len(res.Backups) != 2 {
		t.Fatalf("Replaced=%v Backups=%v, want both binaries and both backups reported", res.Replaced, res.Backups)
	}
}

// TestRunContentFallbackWithoutPublishedChecksum covers a release with neither
// an asset digest nor a body checksum: the archive contents must carry the
// verification, and the output has to say which of the two happened.
func TestRunContentFallbackWithoutPublishedChecksum(t *testing.T) {
	f := newFake(t, "v0.32.0", map[string][]byte{
		"leankg":       stampedBin("0.32.0"),
		"leankg-embed": stampedBin("0.32.0"),
	})
	o, dir := f.opts(t, "0.31.0")
	res := mustRun(t, o)
	if res.Status != StatusUpdated {
		t.Fatalf("Status = %q", res.Status)
	}
	if res.ChecksumVerified {
		t.Fatal("ChecksumVerified = true although nothing was published")
	}
	for _, want := range []string{"no checksum", "leankg", "leankg-embed", "0.32.0"} {
		if !strings.Contains(res.Notice, want) {
			t.Fatalf("Notice %q must name the fallback and what it checked (missing %q)", res.Notice, want)
		}
	}
	if !bytes.Contains(mustRead(t, filepath.Join(dir, "leankg-embed")), []byte("0.32.0")) {
		t.Fatal("leankg-embed was not replaced")
	}
}

// TestRunRefusesWrongVersionArchive is the point of the content fallback: an
// archive that arrives intact but holds a different build must not be installed.
func TestRunRefusesWrongVersionArchive(t *testing.T) {
	f := newFake(t, "v0.32.0", map[string][]byte{
		"leankg":       stampedBin("0.31.0"), // a stale build repackaged under a new tag
		"leankg-embed": stampedBin("0.31.0"),
	})
	o, dir := f.opts(t, "0.31.0")
	_, err := Run(context.Background(), o)
	if err == nil {
		t.Fatal("Run installed an archive stamped with the wrong version")
	}
	if !strings.Contains(err.Error(), "not stamped with version 0.32.0") {
		t.Fatalf("error must say what failed: %v", err)
	}
	assertInstallUntouched(t, dir, "0.31.0")
}

func TestRunRejectsCorruptArchive(t *testing.T) {
	f := newFake(t, "v0.32.0", map[string][]byte{
		"leankg":       stampedBin("0.32.0"),
		"leankg-embed": stampedBin("0.32.0"),
	})
	f.tgz = f.tgz[:len(f.tgz)/3] // truncated mid-stream: a partial download
	o, dir := f.opts(t, "0.31.0")
	_, err := Run(context.Background(), o)
	if err == nil {
		t.Fatal("Run accepted a truncated archive")
	}
	if !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("error must name the corruption: %v", err)
	}
	assertInstallUntouched(t, dir, "0.31.0")
}

func TestRunRejectsChecksumMismatch(t *testing.T) {
	f := newFake(t, "v0.32.0", map[string][]byte{
		"leankg":       stampedBin("0.32.0"),
		"leankg-embed": stampedBin("0.32.0"),
	})
	f.publishChecksum()
	f.digest = "sha256:" + strings.Repeat("0", 64) // the published value no longer matches
	o, dir := f.opts(t, "0.31.0")
	_, err := Run(context.Background(), o)
	if err == nil {
		t.Fatal("Run installed bytes whose checksum does not match the release")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("error must say what mismatched: %v", err)
	}
	assertInstallUntouched(t, dir, "0.31.0")
}

func TestRunRefusesUnwritableDir(t *testing.T) {
	f := newFake(t, "v0.32.0", map[string][]byte{
		"leankg":       stampedBin("0.32.0"),
		"leankg-embed": stampedBin("0.32.0"),
	})
	o, dir := f.opts(t, "0.31.0")
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Skipf("cannot revoke write permission: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	_, err := Run(context.Background(), o)
	var pe *PermError
	if !errors.As(err, &pe) {
		t.Fatalf("Run error = %v, want a *PermError", err)
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("the error must unwrap to fs.ErrPermission, got %v", err)
	}
	want := "sudo " + filepath.Join(dir, "leankg") + " update"
	if pe.Command != want {
		t.Fatalf("Command = %q, want the exact re-run %q", pe.Command, want)
	}
	if !strings.Contains(pe.Error(), want) {
		t.Fatalf("Error() must print the re-run command for the user: %q", pe.Error())
	}
	if n := f.downloads.Load(); n != 0 {
		t.Fatalf("downloads = %d; the preflight must fail before fetching %s", n, f.asset().Name)
	}
	os.Chmod(dir, 0o755)
	assertInstallUntouched(t, dir, "0.31.0")
}

func TestRunCheckModes(t *testing.T) {
	f := newFake(t, "v0.32.0", map[string][]byte{
		"leankg":       stampedBin("0.32.0"),
		"leankg-embed": stampedBin("0.32.0"),
	})
	behind, dir := f.opts(t, "0.31.0")
	behind.Check = true
	res := mustRun(t, behind)
	if res.Status != StatusCheckedBehind {
		t.Fatalf("Status = %q, want %q (the verb maps it to exit 1)", res.Status, StatusCheckedBehind)
	}
	if res.Current != "0.31.0" || res.Latest != "0.32.0" {
		t.Fatalf("--check must report both versions, got %q / %q", res.Current, res.Latest)
	}
	if n := f.downloads.Load(); n != 0 {
		t.Fatalf("--check downloaded %d times", n)
	}
	assertInstallUntouched(t, dir, "0.31.0")

	current, _ := f.opts(t, "0.32.0")
	current.Check = true
	if res := mustRun(t, current); res.Status != StatusCheckedCurrent {
		t.Fatalf("Status = %q, want %q", res.Status, StatusCheckedCurrent)
	}
}

func TestRunMissingAssetForPlatform(t *testing.T) {
	f := newFake(t, "v0.32.0", map[string][]byte{
		"leankg":       stampedBin("0.32.0"),
		"leankg-embed": stampedBin("0.32.0"),
	})
	o, dir := f.opts(t, "0.31.0")
	o.GOOS, o.GOARCH = "windows", "amd64"
	_, err := Run(context.Background(), o)
	if err == nil {
		t.Fatal("Run must fail for a platform the release does not build")
	}
	if !strings.Contains(err.Error(), "windows-amd64") || !strings.Contains(err.Error(), "leankg-linux-amd64.tgz") {
		t.Fatalf("error must name the missing asset and what the release does publish: %v", err)
	}
	assertInstallUntouched(t, dir, "0.31.0")
}

func TestRunBoundsDownloadSize(t *testing.T) {
	f := newFake(t, "v0.32.0", map[string][]byte{
		"leankg":       stampedBin("0.32.0"),
		"leankg-embed": stampedBin("0.32.0"),
	})
	o, dir := f.opts(t, "0.31.0")
	o.MaxBytes = 4 // smaller than any gzip stream
	_, err := Run(context.Background(), o)
	if err == nil {
		t.Fatal("Run accepted an archive over the size ceiling")
	}
	if !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("error must name the limit: %v", err)
	}
	assertInstallUntouched(t, dir, "0.31.0")
}

func TestUntarRejectsUnexpectedArchives(t *testing.T) {
	head := stampedBin("1.0.0")
	// pack writes a member's declared bytes as zeros (tar requires Size bytes);
	// untarGzip rejects an oversized member before reading any payload, so
	// huge declared sizes are cheap here.
	pack := func(hdrs ...*tar.Header) []byte {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		for _, h := range hdrs {
			if err := tw.WriteHeader(h); err != nil {
				t.Fatal(err)
			}
			if h.Typeflag == tar.TypeReg && h.Size > 0 {
				payload := make([]byte, h.Size)
				if h.Size <= int64(len(head)) {
					payload = head[:h.Size]
				}
				if _, err := tw.Write(payload); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	reg := func(name string, size int64) *tar.Header {
		return &tar.Header{Name: name, Mode: 0o755, Typeflag: tar.TypeReg, Size: size}
	}
	both := func() []*tar.Header { return []*tar.Header{reg("leankg", 16), reg("leankg-embed", 16)} }

	for _, tc := range []struct {
		name string
		arch []byte
		want string
	}{
		{"not gzip", []byte("plain text, not a tarball"), "not valid gzip"},
		{"missing embed binary", pack(reg("leankg", 16)), "missing the leankg-embed binary"},
		{"symlink member", pack(append(both(), &tar.Header{Name: "evil", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})...), "symlink"},
		{"directory member", pack(append(both(), &tar.Header{Name: "subdir", Typeflag: tar.TypeDir})...), "directory"},
		{"parent traversal", pack(append(both(), reg("../evil", 16))...), "unexpected member"},
		{"absolute member", pack(append(both(), reg("/etc/passwd", 16))...), "unexpected member"},
		{"dot-slash member", pack(append(both(), reg("./leankg-embed", 16))...), "unexpected member"},
		{"unknown member", pack(append(both(), reg("innocent.txt", 16))...), "unexpected member"},
		{"duplicate member", pack(reg("leankg", 16), reg("leankg", 16), reg("leankg-embed", 16)), "twice"},
		{"oversized member", pack(reg("leankg", maxMember+16), reg("leankg-embed", 16)), "declares"},
	} {
		if _, err := untarGzip(tc.arch); err == nil {
			t.Errorf("%s: untarGzip accepted it", tc.name)
		} else if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not say %q", tc.name, err, tc.want)
		}
	}
	// The honest archive unpacks to exactly the two members, byte for byte.
	got, err := untarGzip(pack(both()...))
	if err != nil {
		t.Fatalf("untarGzip rejected a well-formed release archive: %v", err)
	}
	if len(got) != 2 || !bytes.Equal(got["leankg"], head[:16]) || !bytes.Equal(got["leankg-embed"], head[:16]) {
		t.Fatalf("members = %d with unexpected contents", len(got))
	}
}

// TestRunRestoresFirstBinaryWhenSecondSwapFails covers the cross-file rollback:
// leankg swaps in, leankg-embed cannot (a directory sits at its target), so the
// first must be put back from its backup rather than the install left mixed.
func TestRunRestoresFirstBinaryWhenSecondSwapFails(t *testing.T) {
	f := newFake(t, "v0.32.0", map[string][]byte{
		"leankg":       stampedBin("0.32.0"),
		"leankg-embed": stampedBin("0.32.0"),
	})
	o, dir := f.opts(t, "0.31.0")
	target := filepath.Join(dir, "leankg-embed")
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), o); err == nil {
		t.Fatal("Run succeeded although leankg-embed could not be replaced")
	}
	if got := mustRead(t, filepath.Join(dir, "leankg")); !bytes.Contains(got, []byte("0.31.0")) {
		t.Fatalf("leankg was left on the new build while leankg-embed is still the old one: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "leankg"+backupSuffix)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the backup should have been renamed back into place: %v", err)
	}
}

func TestInstallBinaryBacksUpAndPreservesMode(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "leankg")
	if err := os.WriteFile(target, stampedBin("0.31.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	bk, err := installBinary(target, stampedBin("0.32.0"))
	if err != nil {
		t.Fatalf("installBinary: %v", err)
	}
	if bk != target+backupSuffix {
		t.Fatalf("backup = %q, want %q", bk, target+backupSuffix)
	}
	st, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o755 {
		t.Fatalf("installed mode = %v, want 0755", st.Mode().Perm())
	}
	if !bytes.Contains(mustRead(t, target), []byte("0.32.0")) {
		t.Fatal("the target does not hold the new bytes")
	}
	// A second swap backs up what the first installed.
	if _, err := installBinary(target, stampedBin("0.33.0")); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, target+backupSuffix); !bytes.Contains(got, []byte("0.32.0")) {
		t.Fatalf(".old holds %q, want the 0.32.0 build", got)
	}
}

func TestInstallBinaryOnFreshTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "leankg")
	bk, err := installBinary(target, stampedBin("0.32.0"))
	if err != nil {
		t.Fatalf("installBinary on a fresh target: %v", err)
	}
	if bk != "" {
		t.Fatalf("backup = %q, want empty: nothing existed to back up", bk)
	}
	if got := entries(t, dir); len(got) != 1 {
		t.Fatalf("install dir = %v, want only the new binary", got)
	}
}

// TestPermErrorContract keeps the errors.Is / errors.As pair the verb relies on.
func TestPermErrorContract(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &PermError{Dir: "/usr/local/bin", Command: "sudo leankg update", Err: fs.ErrPermission})
	var pe *PermError
	if !errors.As(err, &pe) {
		t.Fatalf("errors.As could not find *PermError in %v", err)
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatal("a *PermError must unwrap to fs.ErrPermission")
	}
	if !strings.Contains(pe.Error(), "sudo leankg update") {
		t.Fatalf("Error() = %q, want it to carry the re-run command", pe.Error())
	}
}

// TestOptionsDefaultsAreReal guards the production wiring: with no injections the
// package must target the actual repo, API, and this platform.
func TestOptionsDefaultsAreReal(t *testing.T) {
	o := Options{}
	o.applyDefaults()
	if o.Repo != DefaultRepo || o.APIBase != DefaultAPIBase {
		t.Fatalf("defaults = %q / %q", o.Repo, o.APIBase)
	}
	if o.GOOS == "" || o.GOARCH == "" || o.ExePath == "" {
		t.Fatalf("platform defaults empty: %q %q %q", o.GOOS, o.GOARCH, o.ExePath)
	}
	if o.MaxBytes != defaultMaxBytes {
		t.Fatalf("MaxBytes = %d, want %d", o.MaxBytes, defaultMaxBytes)
	}
	if o.Client == nil || o.Logf == nil {
		t.Fatal("Client and Logf must default to working values")
	}
	if want := fmt.Sprintf("leankg-%s-%s.tgz", o.GOOS, o.GOARCH); assetName(o.GOOS, o.GOARCH) != want {
		t.Fatalf("assetName = %q, want %q", assetName(o.GOOS, o.GOARCH), want)
	}
}

// TestSudoCommandShape is the user-facing half of the EACCES path.
func TestSudoCommandShape(t *testing.T) {
	if got := sudoCommand("/usr/local/bin/leankg"); got != "sudo /usr/local/bin/leankg update" {
		t.Fatalf("sudoCommand = %q", got)
	}
	if got := sudoCommand("leankg"); got != "sudo leankg update" {
		t.Fatalf("sudoCommand = %q", got)
	}
}

// TestDownloadRejectsNon200 keeps an error page from being installed as a binary.
func TestDownloadRejectsNon200(t *testing.T) {
	f := newFake(t, "v0.32.0", map[string][]byte{"leankg": stampedBin("0.32.0"), "leankg-embed": stampedBin("0.32.0")})
	o, _ := f.opts(t, "0.31.0")
	o.MaxBytes = defaultMaxBytes
	if _, err := download(context.Background(), &o, f.srv.URL+"/nope"); err == nil ||
		!strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("download of a missing asset = %v, want an HTTP 404 error", err)
	}
}
