package sources

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recorder captures progress messages for assertions.
type recorder struct{ messages []string }

func (r *recorder) Report(message string) { r.messages = append(r.messages, message) }

func (r *recorder) contains(substr string) bool {
	for _, m := range r.messages {
		if strings.Contains(m, substr) {
			return true
		}
	}
	return false
}

func TestParseURI(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want URI
	}{
		{"gs bucket only", "gs://my-bucket", URI{Kind: KindGCS, Bucket: "my-bucket"}},
		{"gs with prefix", "gs://my-bucket/path/to/code", URI{Kind: KindGCS, Bucket: "my-bucket", Prefix: "path/to/code"}},
		{"gs trailing slash", "gs://my-bucket/", URI{Kind: KindGCS, Bucket: "my-bucket"}},
		{"s3 bucket and prefix", "s3://my-bucket/prefix", URI{Kind: KindS3, Bucket: "my-bucket", Prefix: "prefix"}},
		{"git https", "git+https://github.com/user/repo.git", URI{Kind: KindGit, URL: "https://github.com/user/repo.git"}},
		{"git ssh", "git+ssh://git@github.com/user/repo.git", URI{Kind: KindGit, URL: "ssh://git@github.com/user/repo.git"}},
		{"git file transport", "git+file:///srv/repo.git", URI{Kind: KindGit, URL: "file:///srv/repo.git"}},
		{"sftp explicit port", "sftp://user@host:2222/path", URI{Kind: KindSFTP, User: "user", Host: "host", Port: 2222, Path: "/path"}},
		{"sftp default port", "sftp://user@host/path", URI{Kind: KindSFTP, User: "user", Host: "host", Port: 22, Path: "/path"}},
		{"sftp no path", "sftp://user@host:2222", URI{Kind: KindSFTP, User: "user", Host: "host", Port: 2222, Path: "/"}},
		{"sftp plus-signed port", "sftp://user@host:+22/p", URI{Kind: KindSFTP, User: "user", Host: "host", Port: 22, Path: "/p"}},
		{"gdrive", "gdrive://abc123folder", URI{Kind: KindGoogleDrive, FolderID: "abc123folder"}},
		{"local relative", "./my-code", URI{Kind: KindLocal, Path: "./my-code"}},
		{"local absolute", "/abs/path", URI{Kind: KindLocal, Path: "/abs/path"}},
		{"unknown scheme falls back to a path", "http://example.com/x", URI{Kind: KindLocal, Path: "http://example.com/x"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseURI(tc.in)
			if err != nil {
				t.Fatalf("ParseURI(%q) error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ParseURI(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseURIRefusesMalformedForms(t *testing.T) {
	for _, in := range []string{
		"git+",                        // no URL after the scheme
		"sftp://host/path",            // no user@
		"sftp://user@host:notaport/p", // port is not a number
		"sftp://user@host:/path",      // empty port
		"sftp://user@host:65536/path", // above u16
	} {
		t.Run(in, func(t *testing.T) {
			got, err := ParseURI(in)
			if !errors.Is(err, ErrInvalidURI) {
				t.Fatalf("ParseURI(%q) = %+v, %v; want ErrInvalidURI", in, got, err)
			}
			if !strings.Contains(err.Error(), "invalid source URI: "+in) {
				t.Fatalf("error text %q does not quote the URI", err)
			}
		})
	}
}

func TestCreate(t *testing.T) {
	t.Run("local", func(t *testing.T) {
		src, err := Create(URI{Kind: KindLocal, Path: "./src"}, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if src.Name() != "local" {
			t.Fatalf("name = %q, want local", src.Name())
		}
	})
	t.Run("gcs", func(t *testing.T) {
		src, err := Create(URI{Kind: KindGCS, Bucket: "bkt", Prefix: "pre"}, "token", "")
		if err != nil {
			t.Fatal(err)
		}
		if src.Name() != "gcs" {
			t.Fatalf("name = %q, want gcs", src.Name())
		}
	})
	t.Run("git defaults to the main ref", func(t *testing.T) {
		src, err := Create(URI{Kind: KindGit, URL: "https://example.com/repo.git"}, "token", "")
		if err != nil {
			t.Fatal(err)
		}
		git, ok := src.(*GitSource)
		if !ok {
			t.Fatalf("source is %T, want *GitSource", src)
		}
		if git.Name() != "git" || git.RefName != "main" || git.URL != "https://example.com/repo.git" || git.Auth != "token" {
			t.Fatalf("source = %+v", git)
		}
	})
	t.Run("git keeps an explicit ref", func(t *testing.T) {
		src, err := Create(URI{Kind: KindGit, URL: "https://example.com/repo.git"}, "", "develop")
		if err != nil {
			t.Fatal(err)
		}
		if src.(*GitSource).RefName != "develop" {
			t.Fatalf("ref = %q, want develop", src.(*GitSource).RefName)
		}
	})
	// The Rust factory refused these three for the same missing dependencies.
	for _, tc := range []struct {
		uri  URI
		want string
	}{
		{URI{Kind: KindS3, Bucket: "b", Prefix: "p"}, "s3 requires aws-sigv4 dependency (Phase 5)"},
		{URI{Kind: KindSFTP, User: "u", Host: "h", Port: 22, Path: "/"}, "sftp requires ssh2 dependency (Phase 6)"},
		{URI{Kind: KindGoogleDrive, FolderID: "f"}, "gdrive requires OAuth + Drive API (Phase 7)"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			_, err := Create(tc.uri, "", "")
			if !errors.Is(err, ErrUnsupportedScheme) {
				t.Fatalf("Create(%+v) error = %v, want ErrUnsupportedScheme", tc.uri, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestStagingDir(t *testing.T) {
	tests := []struct {
		name  string
		uri   URI
		check func(t *testing.T, dir string)
	}{
		{
			name: "git",
			uri:  URI{Kind: KindGit, URL: "https://github.com/user/repo.git"},
			check: func(t *testing.T, dir string) {
				if !strings.HasPrefix(dir, "git_") {
					t.Fatalf("dir = %q, want a git_ prefix", dir)
				}
			},
		},
		{
			name: "gcs bucket only",
			uri:  URI{Kind: KindGCS, Bucket: "bkt"},
			check: func(t *testing.T, dir string) {
				if dir != "gs_bkt" {
					t.Fatalf("dir = %q, want gs_bkt", dir)
				}
			},
		},
		{
			name: "gcs with prefix",
			uri:  URI{Kind: KindGCS, Bucket: "bkt", Prefix: "a/b"},
			check: func(t *testing.T, dir string) {
				if dir != "gs_bkt_a_b" {
					t.Fatalf("dir = %q, want gs_bkt_a_b", dir)
				}
			},
		},
		{
			name: "sftp",
			uri:  URI{Kind: KindSFTP, User: "u", Host: "h", Port: 2222, Path: "/p"},
			check: func(t *testing.T, dir string) {
				if dir != "sftp_u_h_2222_p" {
					t.Fatalf("dir = %q, want sftp_u_h_2222_p", dir)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := StagingDir(tc.uri)
			// Every separator and scheme colon is replaced, so the name is
			// always a single path component.
			if strings.ContainsAny(dir, `/\:`) {
				t.Fatalf("dir = %q, must not contain a path separator or scheme colon", dir)
			}
			tc.check(t, dir)
		})
	}
}

func TestStagingDirTraversalCannotEscape(t *testing.T) {
	root := t.TempDir()
	for _, uri := range []URI{
		{Kind: KindGit, URL: "https://host/../../etc/passwd"},
		{Kind: KindGit, URL: "../.."},
		{Kind: KindGCS, Bucket: "..", Prefix: "../../x"},
		{Kind: KindLocal, Path: "/etc/passwd"},
	} {
		dir, err := stagingPath(root, uri)
		if err != nil {
			t.Fatalf("stagingPath(%+v) error: %v", uri, err)
		}
		if filepath.Dir(dir) != filepath.Clean(root) {
			t.Fatalf("stagingPath(%+v) = %q, not a direct child of %q", uri, dir, root)
		}
	}
}

func TestResolveReportsInvalidURIs(t *testing.T) {
	_, err := Resolve(context.Background(), t.TempDir(), "git+", "", "", nil)
	if !errors.Is(err, ErrInvalidURI) {
		t.Fatalf("Resolve error = %v, want ErrInvalidURI", err)
	}
	if !strings.Contains(err.Error(), `invalid source URI "git+"`) {
		t.Fatalf("error = %q", err)
	}

	_, err = Resolve(context.Background(), t.TempDir(), "s3://bkt/pre", "", "", nil)
	if !errors.Is(err, ErrUnsupportedScheme) {
		t.Fatalf("Resolve error = %v, want ErrUnsupportedScheme", err)
	}
	if !strings.Contains(err.Error(), `cannot create source for "s3://bkt/pre"`) {
		t.Fatalf("error = %q", err)
	}
}

func TestResolveLocalSourceUsesTheTreeInPlace(t *testing.T) {
	project := t.TempDir()
	tree := filepath.Join(project, "code")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	// A relative --source resolves against the working directory (Rust
	// LocalSource), not against the project root.
	t.Chdir(project)
	got, err := Resolve(context.Background(), project, "./code", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(tree)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Resolve = %q, want %q", got, want)
	}
	// The staging root is still the documented location.
	if _, err := os.Stat(filepath.Join(project, ".leankg", "sources")); err != nil {
		t.Fatalf("staging root: %v", err)
	}
}

func TestMaxFileSizeBytes(t *testing.T) {
	if got := MaxFileSizeBytes(); got != 2*1024*1024 {
		t.Fatalf("default = %d, want 2 MiB", got)
	}
	t.Setenv("LEANKG_MAX_FILE_SIZE", "4096")
	if got := MaxFileSizeBytes(); got != 4096 {
		t.Fatalf("override = %d, want 4096", got)
	}
	t.Setenv("LEANKG_MAX_FILE_SIZE", "not-a-number")
	if got := MaxFileSizeBytes(); got != 2*1024*1024 {
		t.Fatalf("unparseable = %d, want the 2 MiB default", got)
	}
}

func TestProgressToleratesNilReporter(t *testing.T) {
	// Library callers must not need a reporter; a nil one is a no-op.
	report(nil, "ignored")
	src := &LocalSource{Path: "."}
	if _, err := src.SyncToLocal(context.Background(), "", nil); err != nil {
		t.Fatalf("SyncToLocal with a nil reporter: %v", err)
	}
	CLIProgress{}.Report("testing 1-2-3")
}

func TestLocalSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	src := &LocalSource{Path: "./sub"}
	got, err := src.SyncToLocal(context.Background(), "/ignored", nil)
	if err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(filepath.Join(dir, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	if got != real {
		t.Fatalf("SyncToLocal = %q, want %q", got, real)
	}

	if fp, err := src.RemoteFingerprint(context.Background()); err != nil || fp != "" {
		t.Fatalf("RemoteFingerprint = %q, %v; want an empty fingerprint and no error", fp, err)
	}
	ephemeral, err := src.MaterializeEphemeral(context.Background(), "/ignored", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ephemeral != got {
		t.Fatalf("MaterializeEphemeral = %q, want the same path as SyncToLocal (%q)", ephemeral, got)
	}
}
