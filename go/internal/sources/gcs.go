// GCS source (Rust src/sources/gcs.rs): sync a bucket prefix into the staging
// directory through the JSON API, poll a fingerprint without downloading, and
// delta-materialize.
//
// Auth is a pre-fetched OAuth2 access token (Rust refused service-account JWT
// signing, which needs rsa/pkcs8): --auth, else GCS_ACCESS_TOKEN. When
// STORAGE_EMULATOR_HOST is set the source targets that base URL and sends the
// literal token "emulator", exactly like the Rust code, so a fake-gcs-server
// works without credentials.
//
// Deviations from the Rust reference, both at the trust boundary (a remote
// listing and its objects are untrusted input):
//   - a non-2xx object download is an error instead of being written to disk
//     (Rust wrote the error body as file content);
//   - object names that would land outside the staging directory, or that map
//     to no path at all, are refused instead of joined onto it.
package sources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	gcsDefaultEndpoint  = "https://storage.googleapis.com/storage/v1/b"
	gcsListTimeout      = 30 * time.Second
	gcsDownloadTimeout  = 120 * time.Second
	gcsListResponseSize = 32 << 20
	gcsPageSize         = 1000
)

// gcsHTTPClient is shared so list/download re-use connections across watch
// polls; every request carries its own deadline.
var gcsHTTPClient = &http.Client{}

// GcsSource is a `gs://bucket/prefix` source.
type GcsSource struct {
	Bucket string
	Prefix string
	Auth   string
}

// Name implements Source.
func (g *GcsSource) Name() string { return "gcs" }

// Endpoint resolves the JSON API base: GCS_ENDPOINT, else STORAGE_EMULATOR_HOST
// (trailing slashes trimmed), else the public API (Rust resolve_endpoint).
func (g *GcsSource) Endpoint() string {
	for _, key := range []string{"GCS_ENDPOINT", "STORAGE_EMULATOR_HOST"} {
		if v, ok := os.LookupEnv(key); ok {
			return strings.TrimRight(v, "/") + "/storage/v1/b"
		}
	}
	return gcsDefaultEndpoint
}

// BearerToken resolves the token to send. An emulator endpoint wins and gets
// the literal "emulator" (it accepts anything); then --auth, then
// GCS_ACCESS_TOKEN. Empty means no token could be resolved (Rust Ok(None)).
func (g *GcsSource) BearerToken() string {
	if _, ok := os.LookupEnv("STORAGE_EMULATOR_HOST"); ok {
		return "emulator"
	}
	if strings.TrimSpace(g.Auth) != "" {
		return g.Auth
	}
	if v, ok := os.LookupEnv("GCS_ACCESS_TOKEN"); ok {
		return v
	}
	return ""
}

// SyncToLocal downloads every non-oversized object under the prefix
// (Rust gcs.rs sync_to_local).
func (g *GcsSource) SyncToLocal(ctx context.Context, stagingRoot string, progress ProgressReporter) (string, error) {
	token, err := g.requireToken()
	if err != nil {
		return "", err
	}
	endpoint := g.Endpoint()
	report(progress, fmt.Sprintf("listing gs://%s/%s via %s ...", g.Bucket, g.Prefix, endpoint))

	objects, err := g.listObjects(ctx, endpoint, token)
	if err != nil {
		return "", err
	}
	report(progress, fmt.Sprintf("found %d objects in bucket", len(objects)))
	if len(objects) == 0 {
		return "", fmt.Errorf("no objects found in gs://%s/%s", g.Bucket, g.Prefix)
	}

	localDir, err := stagingPath(stagingRoot, g.uri())
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		return "", err
	}

	maxSize := MaxFileSizeBytes()
	downloaded, totalBytes := 0, 0
	for _, obj := range objects {
		target, err := g.resolveTarget(localDir, obj.Name)
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		body, err := g.download(ctx, endpoint, token, obj.Name)
		if err != nil {
			return "", err
		}
		if uint64(len(body)) > maxSize {
			report(progress, fmt.Sprintf("skipping oversized object %s (%d bytes)", obj.Name, len(body)))
			continue
		}
		if err := os.WriteFile(target, body, 0o644); err != nil {
			return "", err
		}
		downloaded++
		totalBytes += len(body)
		if downloaded%100 == 0 || downloaded == len(objects) {
			report(progress, fmt.Sprintf("synced %d/%d objects (%d MiB)", downloaded, len(objects), totalBytes/(1024*1024)))
		}
	}
	report(progress, fmt.Sprintf("complete: %d objects, %d MiB -> %s", downloaded, totalBytes/(1024*1024), localDir))
	return localDir, nil
}

// RemoteFingerprint hashes the (name, etag) listing so a watch poll can skip an
// unchanged bucket. An empty listing yields "" (Rust Ok(None)).
//
// Rust's doc comment claimed a sorted digest but hashed listing order; this
// port keeps listing order, which is the observable behavior.
func (g *GcsSource) RemoteFingerprint(ctx context.Context) (string, error) {
	objects, err := g.listObjects(ctx, g.Endpoint(), g.BearerToken())
	if err != nil {
		return "", err
	}
	if len(objects) == 0 {
		return "", nil
	}
	h := sha256.New()
	for _, obj := range objects {
		fmt.Fprintf(h, "%s\x00%s\n", obj.Name, obj.ETag)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// MaterializeEphemeral delta-syncs: it downloads the current objects, removes
// local files that vanished remotely, and prunes the empty directories left
// behind (Rust gcs.rs materialize_ephemeral).
func (g *GcsSource) MaterializeEphemeral(ctx context.Context, stagingRoot string, progress ProgressReporter) (string, error) {
	token, err := g.requireToken()
	if err != nil {
		return "", err
	}
	localDir, err := stagingPath(stagingRoot, g.uri())
	if err != nil {
		return "", err
	}
	endpoint := g.Endpoint()
	objects, err := g.listObjects(ctx, endpoint, token)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		return "", err
	}
	if len(objects) == 0 {
		// An empty bucket means an empty tree.
		if err := os.RemoveAll(localDir); err != nil {
			return "", err
		}
		if err := os.MkdirAll(localDir, 0o755); err != nil {
			return "", err
		}
		report(progress, "delta sync complete: 0 objects")
		return localDir, nil
	}

	maxSize := MaxFileSizeBytes()
	remote := make(map[string]bool, len(objects))
	for _, obj := range objects {
		target, err := g.resolveTarget(localDir, obj.Name)
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		rel, err := filepath.Rel(localDir, target)
		if err != nil {
			return "", err
		}
		remote[filepath.ToSlash(rel)] = true

		// Rust downloads every listed object on every delta pass (the etag is
		// not compared per object); the fingerprint is what gates the call.
		body, err := g.download(ctx, endpoint, token, obj.Name)
		if err != nil {
			return "", err
		}
		if uint64(len(body)) <= maxSize {
			if err := os.WriteFile(target, body, 0o644); err != nil {
				return "", err
			}
		}
	}

	stale, err := staleFiles(localDir, remote)
	if err != nil {
		return "", err
	}
	for _, path := range stale {
		if err := os.Remove(path); err != nil {
			return "", err
		}
		report(progress, "removed stale: "+path)
	}
	pruneEmptyDirs(localDir)

	report(progress, fmt.Sprintf("delta sync complete: %d objects", len(objects)))
	return localDir, nil
}

func (g *GcsSource) uri() URI { return URI{Kind: KindGCS, Bucket: g.Bucket, Prefix: g.Prefix} }

// requireToken returns the bearer token or an actionable ErrAuthRequired.
func (g *GcsSource) requireToken() (string, error) {
	if token := g.BearerToken(); token != "" {
		return token, nil
	}
	return "", fmt.Errorf("%w (GCS: pass --auth <access-token> or set GCS_ACCESS_TOKEN; "+
		"obtain one with `gcloud auth print-access-token`)", ErrAuthRequired)
}

// gcsObject is one listed object: its full name and the revision tag used by
// the fingerprint.
type gcsObject struct {
	Name string
	ETag string
}

// gcsListResponse is the JSON API listing page.
type gcsListResponse struct {
	Items []struct {
		Name       string     `json:"name"`
		Size       sizeString `json:"size"`
		ETag       string     `json:"etag"`
		Generation string     `json:"generation"`
	} `json:"items"`
	NextPageToken string `json:"nextPageToken"`
}

// sizeString accepts the JSON API's string-encoded int64; an unparseable or
// absent size reads as 0, which is what the Rust `as_str().parse().unwrap_or(0)`
// chain produced.
type sizeString int64

// UnmarshalJSON implements json.Unmarshaler.
func (s *sizeString) UnmarshalJSON(b []byte) error {
	text := strings.Trim(string(b), `"`)
	if text == "" || text == "null" {
		return nil
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return nil
	}
	*s = sizeString(n)
	return nil
}

// listObjects pages through the bucket prefix, skipping directory placeholder
// entries (a name ending in "/" with size 0). Rust had two near-identical list
// helpers (with and without object metadata); one function serves both callers
// and always asks for projection=noAcl, which only drops ACL fields.
func (g *GcsSource) listObjects(ctx context.Context, endpoint, token string) ([]gcsObject, error) {
	var objects []gcsObject
	pageToken := ""
	for {
		query := url.Values{}
		if g.Prefix != "" {
			query.Set("prefix", g.Prefix)
		}
		if pageToken != "" {
			query.Set("pageToken", pageToken)
		}
		query.Set("maxResults", strconv.Itoa(gcsPageSize))
		query.Set("projection", "noAcl")

		target := endpoint + "/" + url.PathEscape(g.Bucket) + "/o?" + query.Encode()
		reqCtx, cancel := context.WithTimeout(ctx, gcsListTimeout)
		body, code, err := gcsGet(reqCtx, target, token, gcsListResponseSize)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("GCS list failed: %w", err)
		}
		if code < 200 || code > 299 {
			return nil, fmt.Errorf("GCS list returned %d %s: %s", code, http.StatusText(code), body)
		}

		var parsed gcsListResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("GCS list parse: %w", err)
		}
		for _, item := range parsed.Items {
			// Guard: an item without a name is malformed input; the Rust code
			// would have tried to download the empty object name.
			if item.Name == "" {
				continue
			}
			if strings.HasSuffix(item.Name, "/") && item.Size == 0 {
				continue
			}
			etag := item.ETag
			if etag == "" {
				etag = item.Generation
			}
			objects = append(objects, gcsObject{Name: item.Name, ETag: etag})
		}
		pageToken = parsed.NextPageToken
		if pageToken == "" {
			return objects, nil
		}
	}
}

// download fetches one object's media. The body is read up to the size cap
// plus one byte, which is all the caller needs to detect an oversized object.
func (g *GcsSource) download(ctx context.Context, endpoint, token, name string) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, gcsDownloadTimeout)
	defer cancel()

	target := endpoint + "/" + url.PathEscape(g.Bucket) + "/o/" + percentEncode(name) + "?alt=media"
	body, code, err := gcsGet(reqCtx, target, token, int64(MaxFileSizeBytes())+1)
	if err != nil {
		return nil, fmt.Errorf("download %s failed: %w", name, err)
	}
	if code < 200 || code > 299 {
		return nil, fmt.Errorf("download %s returned %d %s: %s", name, code, http.StatusText(code), strings.TrimSpace(string(body)))
	}
	return body, nil
}

// gcsGet performs one authenticated GET under ctx and returns the body and
// status code. The body is capped at limit: list responses and object media
// have different caps.
func gcsGet(ctx context.Context, target, token string, limit int64) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := gcsHTTPClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

// resolveTarget maps an object name to its staging path, removing the URI
// prefix and refusing names that would land outside the staging directory
// (guard: object names are remote-controlled).
func (g *GcsSource) resolveTarget(localDir, objName string) (string, error) {
	rel := relativeObjectPath(g.Prefix, objName)
	if rel == "" || filepath.Clean(rel) == "." {
		return "", fmt.Errorf("gcs: object %q maps to an empty relative path", objName)
	}
	target := filepath.Join(localDir, filepath.FromSlash(rel))
	root := filepath.Clean(localDir)
	if target != root && !strings.HasPrefix(target, root+string(filepath.Separator)) {
		return "", fmt.Errorf("gcs: object %q escapes the staging directory", objName)
	}
	return target, nil
}

// relativeObjectPath strips the bucket prefix from an object name, mirroring
// Rust's `strip_prefix(prefix).unwrap_or(name).trim_start_matches('/')`.
func relativeObjectPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	if rest, ok := strings.CutPrefix(name, prefix); ok {
		return strings.TrimLeft(rest, "/")
	}
	return name
}

// staleFiles lists the local files under localDir that the remote listing no
// longer contains (staging-relative slash paths).
func staleFiles(localDir string, remote map[string]bool) ([]string, error) {
	var stale []string
	err := filepath.WalkDir(localDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, rerr := filepath.Rel(localDir, path)
		if rerr != nil {
			return rerr
		}
		if !remote[filepath.ToSlash(rel)] {
			stale = append(stale, path)
		}
		return nil
	})
	return stale, err
}

// pruneEmptyDirs removes empty directories bottom-up.
func pruneEmptyDirs(localDir string) {
	var dirs []string
	_ = filepath.WalkDir(localDir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && path != localDir {
			dirs = append(dirs, path)
		}
		return nil
	})
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, dir := range dirs {
		if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
			_ = os.Remove(dir)
		}
	}
}

// percentEncode encodes a GCS object name for the JSON API path: everything
// outside the unreserved set is escaped, and '/' becomes %2F (Rust
// percent_encode).
func percentEncode(input string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(input))
	for i := 0; i < len(input); i++ {
		c := input[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/':
			b.WriteString("%2F")
		default:
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0x0f])
		}
	}
	return b.String()
}
