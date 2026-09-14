// Package sources implements remote source acquisition for the `--source`
// verb flag: parse a URI, build the matching Source, and sync it into a bounded
// staging tree under <project>/.leankg/sources before indexing.
//
// Port map (Rust reference at rev f7624143^):
//
//	src/sources/mod.rs   -> this file (URI parse table, factory, staging names)
//	src/sources/local.rs -> local.go
//	src/sources/git.rs   -> git.go
//	src/sources/gcs.rs   -> gcs.go
//
// Security posture (mirrors the Rust code, with the guards marked "guard"):
//   - Git URLs reach the git binary as argv elements, never through a shell,
//     so a URI cannot inject a command.
//   - The staging directory is a single sanitized path component under
//     <project>/.leankg/sources: every byte outside [A-Za-z0-9._-] (including
//     '/' and ':') becomes '_', so no URI can escape the staging root (guard:
//     stagingPath re-checks that the result is a direct child).
//   - Clones are shallow (`--depth 1`) and ref-pinned, ref defaulting to
//     "main"; the ref is an argv element too.
//   - A token is injected only into https URLs, as `oauth2:<token>@host`
//     (Rust maybe_inject_auth). That is the Rust credential policy, so the
//     token also lands in the clone's .git/config remote URL: keep the staging
//     tree inside the project root and out of version control.
//   - Schemes Rust refused stay refused: s3 (SigV4), sftp (ssh2) and gdrive
//     (OAuth) fail with ErrUnsupportedScheme and the Rust message text.
//   - Remote-controlled paths are validated at the trust boundary: tar entries
//     and GCS object names that would land outside the staging directory are
//     rejected instead of written (guards in git.go/gcs.go).
//
// Staging layout: <project>/.leankg/sources is a dot-directory, so the
// indexer's walk (internal/index skipDirs + dot-prefix rule) never descends
// into the clone's .git during a later index of the project root; the synced
// tree itself is what gets handed to the indexer.
package sources

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Kind discriminates the parsed URI variants (Rust SourceUri).
type Kind uint8

const (
	KindLocal Kind = iota
	KindGCS
	KindS3
	KindGit
	KindSFTP
	KindGoogleDrive
)

// String returns the staging/log label for a kind.
func (k Kind) String() string {
	switch k {
	case KindLocal:
		return "local"
	case KindGCS:
		return "gcs"
	case KindS3:
		return "s3"
	case KindGit:
		return "git"
	case KindSFTP:
		return "sftp"
	case KindGoogleDrive:
		return "gdrive"
	default:
		return "unknown"
	}
}

// URI is one parsed `--source` value. The zero value is not a valid source:
// Kind selects which fields carry meaning.
type URI struct {
	Kind Kind
	// Path is the local filesystem path (KindLocal) or the remote path
	// (KindSFTP).
	Path string
	// Bucket and Prefix describe a KindGCS or KindS3 location; Prefix is
	// empty when the URI named the bucket only.
	Bucket string
	Prefix string
	// URL is the transport URL of a KindGit source, without the `git+` prefix.
	URL string
	// User, Host and Port describe a KindSFTP location (Port defaults to 22).
	User string
	Host string
	Port uint16
	// FolderID is the KindGoogleDrive folder.
	FolderID string
}

// Source errors. Each wraps a Rust SourceError variant, so callers can test
// with errors.Is and print the Rust message text.
var (
	// ErrUnsupportedScheme: "unsupported source URI scheme: {scheme}".
	ErrUnsupportedScheme = errors.New("unsupported source URI scheme")
	// ErrInvalidURI: "invalid source URI: {uri}".
	ErrInvalidURI = errors.New("invalid source URI")
	// ErrAuthRequired: "auth required but not provided for source type: {type}".
	ErrAuthRequired = errors.New("auth required but not provided for source type")
)

// ParseURI parses a `--source` string into a URI.
//
// Supported schemes (Rust src/sources/mod.rs parse_source_uri):
//
//	| Pattern                                    | Kind        |
//	|--------------------------------------------|-------------|
//	| ./path or /abs/path, or any unknown scheme | Local       |
//	| gs://bucket or gs://bucket/prefix          | GCS         |
//	| s3://bucket/prefix                          | S3          |
//	| git+https://host/owner/repo.git, git+ssh:// | Git         |
//	| sftp://user@host:port/path                  | SFTP        |
//	| gdrive://folder-id                          | GoogleDrive |
//
// Refused (ErrInvalidURI), exactly as in Rust: a bare "git+" with no URL, an
// sftp URI without "user@", an sftp port that is not a number (including an
// empty one), and a port above 65535. Anything else is a local path — Rust did
// not reject unknown schemes here, it rejected them in Factory.Create.
//
// Note one edge difference: Rust's u16 parse accepted a leading '+' ("+22");
// Go's strconv rejects it, so this port strips one leading '+' to match.
func ParseURI(uri string) (URI, error) {
	switch {
	case strings.HasPrefix(uri, "gs://"):
		bucket, prefix := splitBucketPrefix(uri[len("gs://"):])
		return URI{Kind: KindGCS, Bucket: bucket, Prefix: prefix}, nil

	case strings.HasPrefix(uri, "s3://"):
		bucket, prefix := splitBucketPrefix(uri[len("s3://"):])
		return URI{Kind: KindS3, Bucket: bucket, Prefix: prefix}, nil

	case strings.HasPrefix(uri, "git+"):
		rest := uri[len("git+"):]
		if rest == "" {
			return URI{}, invalidURI(uri)
		}
		return URI{Kind: KindGit, URL: rest}, nil

	case strings.HasPrefix(uri, "sftp://"):
		rest := uri[len("sftp://"):]
		userHostPort, path := rest, "/"
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			userHostPort, path = rest[:i], rest[i:]
		}
		at := strings.IndexByte(userHostPort, '@')
		if at < 0 {
			return URI{}, invalidURI(uri)
		}
		user, hostPort := userHostPort[:at], userHostPort[at+1:]
		host, port := hostPort, uint16(22)
		if i := strings.IndexByte(hostPort, ':'); i >= 0 {
			n, err := strconv.ParseUint(strings.TrimPrefix(hostPort[i+1:], "+"), 10, 16)
			if err != nil {
				return URI{}, invalidURI(uri)
			}
			host, port = hostPort[:i], uint16(n)
		}
		return URI{Kind: KindSFTP, User: user, Host: host, Port: port, Path: path}, nil

	case strings.HasPrefix(uri, "gdrive://"):
		return URI{Kind: KindGoogleDrive, FolderID: uri[len("gdrive://"):]}, nil

	default:
		return URI{Kind: KindLocal, Path: uri}, nil
	}
}

// Create builds the Source for a parsed URI (Rust SourceFactory::create).
// auth is the resolved credential ("" for none) and refName the git ref
// ("" means "main"; ignored by non-git sources).
func Create(u URI, auth, refName string) (Source, error) {
	switch u.Kind {
	case KindLocal:
		return &LocalSource{Path: u.Path}, nil
	case KindGCS:
		return &GcsSource{Bucket: u.Bucket, Prefix: u.Prefix, Auth: auth}, nil
	case KindS3:
		// The Rust port refused S3 for the same reason: no SigV4 signer.
		return nil, fmt.Errorf("%w: s3 requires aws-sigv4 dependency (Phase 5)", ErrUnsupportedScheme)
	case KindGit:
		ref := refName
		if ref == "" {
			ref = "main"
		}
		return &GitSource{URL: u.URL, Auth: auth, RefName: ref}, nil
	case KindSFTP:
		return nil, fmt.Errorf("%w: sftp requires ssh2 dependency (Phase 6)", ErrUnsupportedScheme)
	case KindGoogleDrive:
		return nil, fmt.Errorf("%w: gdrive requires OAuth + Drive API (Phase 7)", ErrUnsupportedScheme)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedScheme, u.Kind)
	}
}

// Resolve parses uri, builds the source, syncs it into
// <projectRoot>/.leankg/sources and returns the local tree to index. It is the
// shared body of the four Rust call sites (index path resolution main.rs:2352,
// incremental index main.rs:2533, refresh main.rs:1161, watch main.rs:970),
// which Rust repeated verbatim.
func Resolve(ctx context.Context, projectRoot, uri, auth, refName string, progress ProgressReporter) (string, error) {
	u, err := ParseURI(uri)
	if err != nil {
		return "", fmt.Errorf("invalid source URI %q: %w", uri, err)
	}
	src, err := Create(u, auth, refName)
	if err != nil {
		return "", fmt.Errorf("cannot create source for %q: %w", uri, err)
	}
	stagingRoot := filepath.Join(projectRoot, ".leankg", "sources")
	if err := os.MkdirAll(stagingRoot, 0o755); err != nil {
		return "", err
	}
	return src.SyncToLocal(ctx, stagingRoot, progress)
}

// StagingDir computes the staging directory name for a URI: a single path
// component that replaces every non-filesystem-safe byte (Rust uri_staging_dir
// + sanitize_dir_name).
//
// ASCII-only simplification: Rust's char::is_alphanumeric kept non-ASCII
// letters, this port maps them to '_' so the name is identical on every
// filesystem.
func StagingDir(u URI) string {
	var raw string
	switch u.Kind {
	case KindLocal:
		raw = "local_" + u.Path
	case KindGCS:
		if u.Prefix == "" {
			raw = "gs_" + u.Bucket
		} else {
			raw = "gs_" + u.Bucket + "_" + u.Prefix
		}
	case KindS3:
		raw = "s3_" + u.Bucket + "_" + u.Prefix
	case KindGit:
		raw = "git_" + u.URL
	case KindSFTP:
		raw = fmt.Sprintf("sftp_%s@%s:%d%s", u.User, u.Host, u.Port, u.Path)
	case KindGoogleDrive:
		raw = "gdrive_" + u.FolderID
	}
	return sanitizeDirName(raw)
}

// MaxFileSizeBytes is the per-file download cap (2 MiB, the indexer default),
// overridable with LEANKG_MAX_FILE_SIZE (bytes).
func MaxFileSizeBytes() uint64 {
	if v, ok := os.LookupEnv("LEANKG_MAX_FILE_SIZE"); ok {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			return n
		}
	}
	return 2 * 1024 * 1024
}

// ProgressReporter receives human-readable sync progress (Rust
// ProgressReporter).
type ProgressReporter interface {
	Report(message string)
}

// CLIProgress prints "[source] <message>" to stderr, like Rust CliProgress.
type CLIProgress struct{}

// Report implements ProgressReporter.
func (CLIProgress) Report(message string) { fmt.Fprintln(os.Stderr, "[source] "+message) }

// Source is one acquired location (Rust trait Source).
type Source interface {
	// Name is the human-readable source type label.
	Name() string

	// SyncToLocal syncs the remote content into stagingRoot and returns the
	// local directory to index.
	SyncToLocal(ctx context.Context, stagingRoot string, progress ProgressReporter) (string, error)

	// RemoteFingerprint polls the remote revision without materializing it.
	// An empty string means the remote cannot be polled (local paths) or
	// reported nothing (git ref absent, empty bucket) — Rust returned
	// Ok(None) for all three and its watch loop only acted on a non-empty
	// value, so callers treat "" as "no change signal".
	RemoteFingerprint(ctx context.Context) (string, error)

	// MaterializeEphemeral materializes content without a persistent clone
	// (Rust's trait default delegates to SyncToLocal).
	MaterializeEphemeral(ctx context.Context, stagingRoot string, progress ProgressReporter) (string, error)
}

// report tolerates a nil reporter so library callers never need a guard.
func report(p ProgressReporter, message string) {
	if p != nil {
		p.Report(message)
	}
}

// stagingPath resolves the per-URI staging directory under stagingRoot and
// guards that it is a direct child: StagingDir already replaces every path
// separator, this makes a traversal impossible even if that changes.
func stagingPath(stagingRoot string, u URI) (string, error) {
	name := StagingDir(u)
	root := filepath.Clean(stagingRoot)
	dir := filepath.Join(root, name)
	if name == "" || dir == root || filepath.Dir(dir) != root {
		return "", fmt.Errorf("sources: refusing staging path %q outside %s", name, root)
	}
	return dir, nil
}

func invalidURI(uri string) error {
	return fmt.Errorf("%w: %s", ErrInvalidURI, uri)
}

// splitBucketPrefix splits "bucket/prefix" at the first slash; a bucket-only
// rest yields an empty prefix.
func splitBucketPrefix(rest string) (bucket, prefix string) {
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i], rest[i+1:]
	}
	return rest, ""
}

// sanitizeDirName maps every byte outside [A-Za-z0-9._-] to '_'.
func sanitizeDirName(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
			b.WriteByte(c)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}
