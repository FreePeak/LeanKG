package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// DefaultRepo is the repository whose Releases are the update source.
const DefaultRepo = "FreePeak/LeanKG"

// DefaultAPIBase is the GitHub REST root. Overridable so the tests can serve a
// release from an httptest server instead of the network.
const DefaultAPIBase = "https://api.github.com"

// assetPrefix is the tarball stem that .github/workflows/release.yml uploads
// as `leankg-<goos>-<goarch>.tgz`, holding the `leankg` and `leankg-embed`
// binaries at the archive root.
const assetPrefix = "leankg"

// Release is the subset of the GitHub Releases API payload this package reads.
// `digest` is the SHA256 GitHub records when the publisher supplies one; the
// LeanKG workflow publishes none, hence the content-verification fallback.
type Release struct {
	TagName string  `json:"tag_name"`
	Name    string  `json:"name"`
	Body    string  `json:"body"`
	Assets  []Asset `json:"assets"`
}

// Asset is one file attached to a Release.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
	Size               int64  `json:"size"`
}

// Version strips the leading `v` from the tag: tags are `v<version>` because
// release-please-config.json sets include-v-in-tag with include-component-in-tag
// off (and `.release-please-manifest.json` is keyed by package path, which is
// what decides the next version — a key other than "." is never read).
func (r Release) Version() string { return strings.TrimPrefix(strings.TrimSpace(r.TagName), "v") }

// assetName builds the tarball name for one platform.
func assetName(goos, goarch string) string {
	return fmt.Sprintf("%s-%s-%s.tgz", assetPrefix, goos, goarch)
}

// AssetFor returns the release asset for a platform. The workflow builds
// linux/{amd64,arm64} and darwin/{arm64,amd64} only, so an unsupported host (or
// a release made before the matrix existed) simply has no match.
func (r Release) AssetFor(goos, goarch string) (Asset, bool) {
	want := assetName(goos, goarch)
	for _, a := range r.Assets {
		if a.Name == want {
			return a, true
		}
	}
	return Asset{}, false
}

// checksum returns the expected SHA256 (lowercase hex) for an asset: the API
// digest first; otherwise a 64-hex published in the release body on a line that
// also names the asset, so a checksum published for a sibling platform can never
// be applied to ours; otherwise "" when the release publishes no checksum.
func checksum(r Release, a Asset) string {
	if d := strings.ToLower(strings.TrimSpace(a.Digest)); d != "" {
		if hex, ok := strings.CutPrefix(d, "sha256:"); ok {
			return hex
		}
		return "" // a digest over some other algorithm verifies nothing here
	}
	for _, line := range strings.Split(r.Body, "\n") {
		if !strings.Contains(line, a.Name) {
			continue
		}
		if m := sha256Hex.FindStringSubmatch(line); m != nil {
			return strings.ToLower(m[1])
		}
	}
	return ""
}

// sha256Hex is a bare SHA256 hex digest, bounded so a longer (other-algorithm)
// hex run cannot be read as one.
var sha256Hex = regexp.MustCompile(`\b([0-9a-fA-F]{64})\b`)

// Latest fetches the newest release. Without a token this is the unauthenticated
// endpoint, which GitHub limits per IP — surfaced as a warning by Run, not an
// error, because the common case (a single check from a workstation) succeeds.
func Latest(ctx context.Context, client *http.Client, apiBase, repo, token string) (Release, error) {
	url := strings.TrimSuffix(apiBase, "/") + "/repos/" + repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "leankg-update")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := client.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer res.Body.Close()
	// The response is a release listing, not an index: a page cap is a bound on
	// a hostile/buggy endpoint, not a feature.
	body, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return Release{}, fmt.Errorf("reading %s: %w", url, err)
	}
	if res.StatusCode != http.StatusOK {
		return Release{}, apiError(res.StatusCode, url, body)
	}
	var rel Release
	if err := json.Unmarshal(body, &rel); err != nil {
		return Release{}, fmt.Errorf("parsing release from %s: %w", url, err)
	}
	if rel.TagName == "" {
		return Release{}, fmt.Errorf("%s returned no tag_name", url)
	}
	return rel, nil
}

// apiError explains the two failures that are not really about LeanKG: a
// rate-limited anonymous call and a repo with no releases.
func apiError(status int, url string, body []byte) error {
	switch status {
	case http.StatusForbidden, http.StatusTooManyRequests:
		return fmt.Errorf("%s: HTTP %d — the anonymous GitHub API is rate-limited per IP; set GITHUB_TOKEN (or pass --token) and retry%s",
			url, status, apiDetail(body))
	case http.StatusNotFound:
		return fmt.Errorf("%s: HTTP 404 — no published release (or no access to a private one)%s", url, apiDetail(body))
	}
	return fmt.Errorf("%s: HTTP %d%s", url, status, apiDetail(body))
}

func apiDetail(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &e) != nil || e.Message == "" {
		return ""
	}
	return ": " + strings.TrimSpace(e.Message)
}
