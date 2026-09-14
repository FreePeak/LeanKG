// Local filesystem source (Rust src/sources/local.rs): a passthrough that
// resolves the path and hands it to the indexer.
package sources

import (
	"context"
	"os"
	"path/filepath"
)

// LocalSource resolves a local filesystem path. It never copies anything: the
// staging root is ignored, exactly like the Rust implementation.
type LocalSource struct {
	Path string
}

// Name implements Source.
func (l *LocalSource) Name() string { return "local" }

// SyncToLocal resolves Path against the working directory and canonicalizes it
// (Rust canonicalize with a fallback to the joined path when the path does not
// fully resolve, e.g. a dangling symlink target).
func (l *LocalSource) SyncToLocal(_ context.Context, _ string, progress ProgressReporter) (string, error) {
	resolved := l.Path
	if !filepath.IsAbs(resolved) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		resolved = filepath.Join(cwd, resolved)
	}
	canonical := resolved
	if real, err := filepath.EvalSymlinks(resolved); err == nil {
		canonical = real
	}
	report(progress, "local source at "+canonical)
	return canonical, nil
}

// RemoteFingerprint returns "" : a local path cannot be polled (Rust's default
// trait implementation returns Ok(None)).
func (l *LocalSource) RemoteFingerprint(context.Context) (string, error) { return "", nil }

// MaterializeEphemeral delegates to SyncToLocal (Rust's trait default).
func (l *LocalSource) MaterializeEphemeral(ctx context.Context, stagingRoot string, progress ProgressReporter) (string, error) {
	return l.SyncToLocal(ctx, stagingRoot, progress)
}
