package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// unsetenv removes a variable for the test and restores it afterwards. The
// source distinguishes "unset" from "empty", so t.Setenv("X", "") is not
// enough here.
func unsetenv(t *testing.T, key string) {
	t.Helper()
	prev, ok := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(key, prev)
			return
		}
		_ = os.Unsetenv(key)
	})
}

// fakeGCS is an in-memory bucket server speaking the JSON API shapes the source
// uses: /b/<bucket>/o (listing) and /b/<bucket>/o/<name>?alt=media (download).
// Listings page two entries at a time so paging is exercised.
type fakeGCS struct {
	server *httptest.Server

	mu       sync.Mutex
	objects  map[string]string // name -> content
	failNext bool
}

func newFakeGCS(t *testing.T, objects map[string]string) *fakeGCS {
	t.Helper()
	f := &fakeGCS{objects: objects}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeGCS) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext {
		f.failNext = false
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/storage/v1/b/"), "/", 3)
	if len(parts) < 2 || parts[0] != "bkt" || parts[1] != "o" {
		http.NotFound(w, r)
		return
	}
	if r.URL.Query().Get("alt") == "media" {
		if len(parts) != 3 {
			http.NotFound(w, r)
			return
		}
		content, ok := f.objects[parts[2]]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprint(w, content)
		return
	}

	prefix := r.URL.Query().Get("prefix")
	page := int64(1)
	if v := r.URL.Query().Get("pageToken"); v != "" {
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		page = parsed
	}
	var names []string
	for name := range f.objects {
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	const pageSize = 2
	start, end := (page-1)*pageSize, page*pageSize
	if start >= int64(len(names)) {
		fmt.Fprint(w, `{"items":[]}`)
		return
	}
	if end > int64(len(names)) {
		end = int64(len(names))
	}
	resp := struct {
		Items []map[string]string `json:"items"`
		Next  string              `json:"nextPageToken,omitempty"`
	}{}
	for _, name := range names[start:end] {
		resp.Items = append(resp.Items, map[string]string{
			"name": name,
			"size": strconv.Itoa(len(f.objects[name])),
			"etag": `"et-` + name + `"`,
		})
	}
	if end < int64(len(names)) {
		resp.Next = strconv.FormatInt(page+1, 10)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// gcsTestEnv points a source at f with the emulator token and no real
// credentials in the environment.
func gcsTestEnv(t *testing.T, f *fakeGCS) *GcsSource {
	t.Helper()
	t.Setenv("STORAGE_EMULATOR_HOST", f.server.URL)
	unsetenv(t, "GCS_ENDPOINT")
	unsetenv(t, "GCS_ACCESS_TOKEN")
	return &GcsSource{Bucket: "bkt", Prefix: "src"}
}

func TestGcsSourceSyncToLocal(t *testing.T) {
	f := newFakeGCS(t, map[string]string{
		"src/main.go":      "package fixture\n",
		"src/util/util.go": "package util\n",
		"other/ignored.go": "package other\n",
	})
	src := gcsTestEnv(t, f)
	progress := &recorder{}

	got, err := src.SyncToLocal(context.Background(), t.TempDir(), progress)
	if err != nil {
		t.Fatalf("SyncToLocal: %v", err)
	}
	// A prefix-scoped sync materializes prefix-stripped paths only.
	for _, rel := range []string{"main.go", "util/util.go"} {
		content, err := os.ReadFile(filepath.Join(got, rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if len(content) == 0 {
			t.Fatalf("%s is empty", rel)
		}
	}
	if _, err := os.Stat(filepath.Join(got, "ignored.go")); !os.IsNotExist(err) {
		t.Fatalf("object outside the prefix was synced (stat err: %v)", err)
	}
	if !progress.contains("found 2 objects in bucket") {
		t.Fatalf("progress = %q", progress.messages)
	}
}

func TestGcsSourceAuthRequiredWithoutAToken(t *testing.T) {
	unsetenv(t, "STORAGE_EMULATOR_HOST")
	unsetenv(t, "GCS_ACCESS_TOKEN")
	src := &GcsSource{Bucket: "bkt", Prefix: "src"}

	_, err := src.SyncToLocal(context.Background(), t.TempDir(), nil)
	if !strings.Contains(err.Error(), "auth required") {
		t.Fatalf("error = %v, want the auth-required message", err)
	}
}

func TestGcsSourceEmptyBucketFails(t *testing.T) {
	f := newFakeGCS(t, map[string]string{})
	src := gcsTestEnv(t, f)

	_, err := src.SyncToLocal(context.Background(), t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "no objects found in gs://bkt/src") {
		t.Fatalf("error = %v, want the empty-bucket failure", err)
	}
}

func TestGcsSourceDownloadFailurePropagates(t *testing.T) {
	f := newFakeGCS(t, map[string]string{"src/main.go": "package fixture\n"})
	src := gcsTestEnv(t, f)
	staging := t.TempDir()

	// The listing succeeds; the download 500s. A failed download is an error,
	// never file content written to disk.
	f.mu.Lock()
	f.failNext = true
	f.mu.Unlock()
	if _, err := src.SyncToLocal(context.Background(), staging, nil); err == nil {
		t.Fatal("SyncToLocal with a failing download succeeded")
	}
	if _, err := os.Stat(filepath.Join(staging, StagingDir(src.uri()), "main.go")); !os.IsNotExist(err) {
		t.Fatalf("the error body landed on disk (stat err: %v)", err)
	}
}

func TestGcsSourceSkipsOversizedObjects(t *testing.T) {
	f := newFakeGCS(t, map[string]string{"src/main.go": strings.Repeat("x", 64)})
	src := gcsTestEnv(t, f)
	t.Setenv("LEANKG_MAX_FILE_SIZE", "8")

	got, err := src.SyncToLocal(context.Background(), t.TempDir(), &recorder{})
	if err != nil {
		t.Fatalf("SyncToLocal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(got, "main.go")); !os.IsNotExist(err) {
		t.Fatalf("oversized object was written (stat err: %v)", err)
	}
}

func TestGcsSourceFingerprintStableUntilObjectsChange(t *testing.T) {
	f := newFakeGCS(t, map[string]string{"src/a.go": "a"})
	src := gcsTestEnv(t, f)

	first, err := src.RemoteFingerprint(context.Background())
	if err != nil {
		t.Fatalf("RemoteFingerprint: %v", err)
	}
	again, err := src.RemoteFingerprint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || first != again {
		t.Fatalf("fingerprint %q then %q, want a stable non-empty digest", first, again)
	}

	f.mu.Lock()
	f.objects["src/b.go"] = "b"
	f.mu.Unlock()
	after, err := src.RemoteFingerprint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after == first {
		t.Fatal("fingerprint unchanged after the bucket changed")
	}
}

func TestGcsSourceFingerprintEmptyBucketIsEmpty(t *testing.T) {
	f := newFakeGCS(t, map[string]string{})
	src := gcsTestEnv(t, f)

	fp, err := src.RemoteFingerprint(context.Background())
	if err != nil {
		t.Fatalf("RemoteFingerprint: %v", err)
	}
	if fp != "" {
		t.Fatalf("fingerprint = %q, want empty for an empty bucket", fp)
	}
}

func TestGcsSourceMaterializeEphemeralDeltaSync(t *testing.T) {
	f := newFakeGCS(t, map[string]string{"src/a.go": "a", "src/b.go": "b"})
	src := gcsTestEnv(t, f)
	staging := t.TempDir()

	got, err := src.MaterializeEphemeral(context.Background(), staging, &recorder{})
	if err != nil {
		t.Fatalf("MaterializeEphemeral: %v", err)
	}
	for _, rel := range []string{"a.go", "b.go"} {
		if _, err := os.Stat(filepath.Join(got, rel)); err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
	}

	// The bucket changes: a.go vanishes, c.go appears. A delta pass must
	// delete the stale file and fetch the new one.
	f.mu.Lock()
	delete(f.objects, "src/a.go")
	f.objects["src/c.go"] = "c"
	f.mu.Unlock()
	got2, err := src.MaterializeEphemeral(context.Background(), staging, &recorder{})
	if err != nil {
		t.Fatalf("second MaterializeEphemeral: %v", err)
	}
	if got2 != got {
		t.Fatalf("second sync dir = %q, want %q", got2, got)
	}
	if _, err := os.Stat(filepath.Join(got, "a.go")); !os.IsNotExist(err) {
		t.Fatalf("stale a.go survived the delta sync (stat err: %v)", err)
	}
	if _, err := os.Stat(filepath.Join(got, "b.go")); err != nil {
		t.Fatalf("unchanged b.go was deleted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(got, "c.go")); err != nil {
		t.Fatalf("new c.go missing: %v", err)
	}
}

func TestGcsSourceMaterializeEphemeralEmptyBucketClearsTheTree(t *testing.T) {
	f := newFakeGCS(t, map[string]string{"src/a.go": "a"})
	src := gcsTestEnv(t, f)
	staging := t.TempDir()

	got, err := src.MaterializeEphemeral(context.Background(), staging, &recorder{})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.objects = map[string]string{}
	f.mu.Unlock()
	got2, err := src.MaterializeEphemeral(context.Background(), staging, &recorder{})
	if err != nil {
		t.Fatalf("second MaterializeEphemeral: %v", err)
	}
	if got2 != got {
		t.Fatalf("second sync dir = %q, want %q", got2, got)
	}
	entries, err := os.ReadDir(got2)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging after an empty-bucket delta = %v, want no entries", entries)
	}
}

func TestGcsSourceRefusesObjectsOutsideStaging(t *testing.T) {
	src := &GcsSource{Bucket: "bkt", Prefix: "src"}
	dir := t.TempDir()

	for _, name := range []string{"../../etc/passwd", "src/../..", "src/"} {
		if _, err := src.resolveTarget(dir, name); err == nil {
			t.Fatalf("resolveTarget(%q) accepted an escaping object name", name)
		}
	}
	// A same-tree object still resolves.
	target, err := src.resolveTarget(dir, "src/pkg/file.go")
	if err != nil {
		t.Fatalf("resolveTarget(src/pkg/file.go): %v", err)
	}
	if target != filepath.Join(dir, "pkg", "file.go") {
		t.Fatalf("target = %q", target)
	}
}

func TestGcsPercentEncode(t *testing.T) {
	// The JSON API requires '/' escaped as %2F in object names; everything
	// outside the unreserved set is escaped.
	got := percentEncode("dir/sub/file with space.txt")
	want := "dir%2Fsub%2Ffile%20with%20space.txt"
	if got != want {
		t.Fatalf("percentEncode = %q, want %q", got, want)
	}
}

func TestGcsEndpointResolution(t *testing.T) {
	// A trailing slash must not double the path separator.
	t.Setenv("GCS_ENDPOINT", "http://localhost:4443/")
	src := &GcsSource{}
	if got := src.Endpoint(); got != "http://localhost:4443/storage/v1/b" {
		t.Fatalf("Endpoint = %q", got)
	}
	unsetenv(t, "GCS_ENDPOINT")

	// The emulator host wins over the public default.
	t.Setenv("STORAGE_EMULATOR_HOST", "http://localhost:4443")
	if got := src.Endpoint(); got != "http://localhost:4443/storage/v1/b" {
		t.Fatalf("Endpoint = %q", got)
	}
	unsetenv(t, "STORAGE_EMULATOR_HOST")

	if got := src.Endpoint(); got != "https://storage.googleapis.com/storage/v1/b" {
		t.Fatalf("Endpoint = %q", got)
	}
}

func TestGcsBearerTokenResolution(t *testing.T) {
	// An emulator endpoint short-circuits to the literal token.
	t.Setenv("STORAGE_EMULATOR_HOST", "http://localhost:4443")
	src := &GcsSource{Auth: "explicit-token"}
	if got := src.BearerToken(); got != "emulator" {
		t.Fatalf("BearerToken = %q, want emulator", got)
	}
	unsetenv(t, "STORAGE_EMULATOR_HOST")

	if got := src.BearerToken(); got != "explicit-token" {
		t.Fatalf("BearerToken = %q, want explicit-token", got)
	}
	t.Setenv("GCS_ACCESS_TOKEN", "env-token")
	if got := (&GcsSource{}).BearerToken(); got != "env-token" {
		t.Fatalf("BearerToken = %q, want env-token", got)
	}
	unsetenv(t, "GCS_ACCESS_TOKEN")
	if got := (&GcsSource{}).BearerToken(); got != "" {
		t.Fatalf("BearerToken = %q, want empty", got)
	}
}
