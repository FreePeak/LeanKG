package update

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Options is one update run. Every field has a working default (Run fills them
// in), so a test injects exactly what it needs — an httptest server's client and
// base URL, a temp-dir ExePath, pinned GOOS/GOARCH — and everything else stays
// real.
type Options struct {
	// Repo is the `owner/name` to update from.
	Repo string
	// CurrentVersion is the running build's version (cmd/leankg's Version()).
	CurrentVersion string
	// ExePath is the running binary's absolute path; its directory is the
	// install directory.
	ExePath string
	// GOOS/GOARCH select the release asset.
	GOOS, GOARCH string
	// APIBase overrides https://api.github.com (tests point it at httptest).
	APIBase string
	// Token is the GitHub API token; empty means anonymous (warned about).
	Token string
	// Check reports current vs latest and installs nothing.
	Check bool
	// Client is the HTTP client (redirects are net/http's default policy).
	Client *http.Client
	// Logf receives progress lines; nil means silent.
	Logf func(format string, args ...any)
	// MaxBytes bounds the release archive download.
	MaxBytes int64
}

// Status is the outcome of a run.
type Status string

const (
	// StatusUpToDate: the newest release matches the running version.
	StatusUpToDate Status = "up-to-date"
	// StatusUpdated: a newer release was downloaded and installed.
	StatusUpdated Status = "updated"
	// StatusCheckedBehind: --check found a newer release and installed nothing.
	StatusCheckedBehind Status = "behind"
	// StatusCheckedCurrent: --check found the running version current.
	StatusCheckedCurrent Status = "current"
)

// Result reports what a run did, for the verb to print and for tests to assert
// on: the versions involved, what verified the download, and which files were
// replaced and backed up. A run creates nothing in the install directory beyond
// the binaries it lists and their `.old` backups, so a caller can prove no stray
// files are left behind.
type Result struct {
	Status           Status
	Current          string
	Latest           string
	AssetName        string
	ChecksumVerified bool
	Notice           string
	Replaced         []string
	Backups          []string
}

// Run performs the update flow:
//
//  1. latest: resolve the newest release and its asset for this platform;
//  2. compare: equal or newer-than-running needs no download;
//  3. preflight: prove the install directory is writable — an EACCES becomes a
//     *PermError carrying the exact sudo re-run, before anything is fetched;
//  4. download: the platform asset, bounded by MaxBytes;
//  5. verify: the published checksum when the release has one, otherwise the
//     archive contents (both binaries, stamped with the requested version) — and
//     the Result says which happened;
//  6. install: per binary, write a sibling temp file, copy the previous binary
//     aside to `<name>.old`, then rename into place. Never delete-then-copy, and
//     if a later swap fails, the earlier ones are restored from their backups.
//
// Steps 4 and 5 touch no install path, so a corrupt or mismatched archive
// provably leaves the working install untouched. Options.Check stops after 2,
// which is why it needs no write permission at all.
func Run(ctx context.Context, o Options) (Result, error) {
	o.applyDefaults()
	res := Result{Current: o.CurrentVersion}

	exe, err := filepath.Abs(o.ExePath)
	if err != nil {
		return res, fmt.Errorf("resolving the binary path: %w", err)
	}
	dir := filepath.Dir(exe)

	if o.Token == "" {
		o.Logf("no GITHUB_TOKEN set: using the anonymous GitHub API, which is rate-limited per IP")
	}
	rel, err := Latest(ctx, o.Client, o.APIBase, o.Repo, o.Token)
	if err != nil {
		return res, err
	}
	res.Latest = rel.Version()

	cmp, err := Compare(o.CurrentVersion, res.Latest)
	if err != nil {
		return res, err
	}
	switch {
	case cmp >= 0:
		res.Status = StatusUpToDate
		if o.Check {
			res.Status = StatusCheckedCurrent
		}
		if cmp > 0 {
			res.Notice = fmt.Sprintf("the running version %s is newer than the newest release %s; nothing to do", o.CurrentVersion, res.Latest)
		}
		return res, nil
	case o.Check:
		res.Status = StatusCheckedBehind
		return res, nil
	}

	asset, ok := rel.AssetFor(o.GOOS, o.GOARCH)
	if !ok {
		return res, fmt.Errorf("release %s publishes no asset for %s-%s (release assets: %s)",
			rel.TagName, o.GOOS, o.GOARCH, assetList(rel))
	}
	res.AssetName = asset.Name

	if err := requireWritable(dir, exe); err != nil {
		return res, err
	}

	wantSum := checksum(rel, asset)
	o.Logf("downloading %s", asset.Name)
	data, err := download(ctx, &o, asset.BrowserDownloadURL)
	if err != nil {
		return res, err
	}
	if err := verifySum(sumHex(data), wantSum); err != nil {
		return res, err
	}
	members, err := untarGzip(data)
	if err != nil {
		return res, err
	}
	if wantSum == "" {
		// Nothing was published to verify against, so the archive contents are
		// the evidence — and the user is told that in as many words.
		notice, err := verifyMembers(members, res.Latest)
		if err != nil {
			return res, err
		}
		res.Notice = notice
	} else {
		res.ChecksumVerified = true
	}

	type swap struct{ target, backup string }
	var done []swap
	rollback := func() {
		for _, s := range done {
			if s.backup == "" {
				os.Remove(s.target) // there was no previous binary to restore
				continue
			}
			os.Rename(s.backup, s.target)
		}
	}
	for _, name := range binaryNames {
		target := filepath.Join(dir, name)
		if name == filepath.Base(exe) {
			target = exe
		}
		bk, err := installBinary(target, members[name])
		if err != nil {
			rollback()
			return res, fmt.Errorf("installing %s: %w", name, err)
		}
		done = append(done, swap{target: target, backup: bk})
		res.Replaced = append(res.Replaced, target)
		if bk != "" {
			res.Backups = append(res.Backups, bk)
		}
	}
	res.Status = StatusUpdated
	return res, nil
}

// applyDefaults fills unset Options from the environment and runtime: the running
// binary's path, its platform, the real GitHub API, and the download ceiling
// (LEANKG_UPDATE_MAX_MB raises it for an unusual release, never to skip it).
func (o *Options) applyDefaults() {
	if o.Repo == "" {
		o.Repo = DefaultRepo
	}
	if o.ExePath == "" {
		if exe, err := os.Executable(); err == nil {
			o.ExePath = exe
		} else {
			o.ExePath = "leankg"
		}
	}
	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}
	if o.GOARCH == "" {
		o.GOARCH = runtime.GOARCH
	}
	if o.APIBase == "" {
		o.APIBase = DefaultAPIBase
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 10 * time.Minute}
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = defaultMaxBytes
		if mb, err := strconv.Atoi(os.Getenv("LEANKG_UPDATE_MAX_MB")); err == nil && mb > 0 {
			o.MaxBytes = int64(mb) << 20
		}
	}
}

// defaultMaxBytes bounds a release download: the real assets are ~20 MB gzipped
// (see .github/workflows/release.yml), 128 MiB leaves room to grow.
const defaultMaxBytes = 128 << 20

// requireWritable proves the install directory accepts new files before anything
// is downloaded, turning the common "installed under /usr/local/bin without
// sudo" failure into a one-line fix instead of a dead end after a 20 MB fetch.
func requireWritable(dir, exe string) error {
	probe, err := os.CreateTemp(dir, ".leankg-update-probe-*")
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return &PermError{Dir: dir, Command: sudoCommand(exe), Err: err}
		}
		return fmt.Errorf("the install directory %s is not writable: %w", dir, err)
	}
	probe.Close()
	os.Remove(probe.Name())
	return nil
}

// sudoCommand is the exact re-run that fixes a permission failure, built from the
// running binary's absolute path.
func sudoCommand(exe string) string {
	if filepath.IsAbs(exe) {
		return "sudo " + exe + " update"
	}
	return "sudo leankg update"
}

// assetList renders a release's asset names for an error message.
func assetList(r Release) string {
	if len(r.Assets) == 0 {
		return "none"
	}
	names := make([]string, len(r.Assets))
	for i, a := range r.Assets {
		names[i] = a.Name
	}
	return strings.Join(names, ", ")
}
