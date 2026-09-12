// /api/file — bounded project file reads for the CodePanel.
// Ported from the deleted Rust src/web/file_resolve.rs, including the
// Docker multi-root sibling probe (LEANKG_PROJECT_DIRS) and its error
// message catalog.
package web

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// maxFileBytes bounds /api/file reads. The Rust engine read the whole file;
// the dashboard only ever previews single sources, so cap the payload.
// ponytail: silent truncation past 1 MiB — upgrade path is range requests.
const maxFileBytes = 1 << 20

type fileResolveError struct {
	status int
	msg    string
}

// cleanProjectRelativePath strips `./` and maps absolute paths under the
// project root to project-relative (Rust clean_project_relative_path).
func cleanProjectRelativePath(raw, projPath string) string {
	clean := strings.TrimPrefix(raw, "./")
	if projPath == "" {
		return clean
	}
	canonical, err := filepath.Abs(projPath)
	if err != nil {
		canonical = projPath
	}
	if rem, ok := strings.CutPrefix(clean, canonical); ok {
		stripped := strings.TrimLeft(rem, "/")
		if stripped == "" {
			return "."
		}
		return stripped
	}
	return clean
}

func projectDirsFromEnv() []string {
	v := os.Getenv("LEANKG_PROJECT_DIRS")
	if v == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// candidatePath mirrors Rust Path::join semantics: an absolute cleanPath
// replaces the base, a relative one is appended WITHOUT lexical cleaning so
// ".." segments survive until EvalSymlinks (canonicalize) resolves them.
func candidatePath(root, cleanPath string) string {
	switch {
	case cleanPath == "" || cleanPath == ".":
		return root
	case strings.HasPrefix(cleanPath, "/"):
		return cleanPath
	default:
		return root + string(filepath.Separator) + cleanPath
	}
}

// tryUnderRoot resolves cleanPath under root and canonicalizes it.
// ok=false means the path is simply missing under this root (Rust Err(None));
// a non-nil *fileResolveError means directory/outside-project.
func tryUnderRoot(cleanPath, root string) (resolved string, ferr *fileResolveError, ok bool) {
	target := candidatePath(root, cleanPath)
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", nil, false // missing under this root — try the next
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		canonicalRoot = root
	}
	if !withinDir(resolved, canonicalRoot) {
		return "", &fileResolveError{status: http.StatusForbidden, msg: "Access denied: path is outside project directory"}, false
	}
	if st, err := os.Stat(resolved); err == nil && st.IsDir() {
		return "", &fileResolveError{
			status: http.StatusBadRequest,
			msg:    "Path '" + cleanPath + "' is a directory; use /api/graph/expand-service?path=…&all=true to load its subgraph",
		}, false
	}
	return resolved, nil, true
}

func withinDir(p, dir string) bool {
	if p == dir {
		return true
	}
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveReadableFile tries the primary root, then sibling mounts.
func resolveReadableFile(cleanPath, primary string, extras []string) (string, *fileResolveError) {
	roots := []string{primary}
	for _, extra := range extras {
		if extra == primary {
			continue
		}
		dup := false
		for _, r := range roots {
			if r == extra {
				dup = true
				break
			}
		}
		if !dup {
			roots = append(roots, extra)
		}
	}

	traversal := strings.HasPrefix(cleanPath, "/")
	for _, seg := range strings.Split(cleanPath, "/") {
		if seg == ".." {
			traversal = true
		}
	}

	var tried []string
	for _, root := range roots {
		tried = append(tried, candidatePath(root, cleanPath))
		resolved, ferr, found := tryUnderRoot(cleanPath, root)
		switch {
		case found:
			return resolved, nil
		case ferr != nil && ferr.status == http.StatusBadRequest:
			return "", ferr
		case ferr != nil && ferr.status == http.StatusForbidden:
			// Traversal under this root — keep searching siblings only for
			// plain relative paths.
			if traversal {
				return "", ferr
			}
		}
	}
	return "", &fileResolveError{
		status: http.StatusNotFound,
		msg: "File not found '" + cleanPath + "'. Indexed path is missing under the active project root; " +
			"tried " + strings.Join(tried, ", ") + ". If this path belongs to another mount in LEANKG_PROJECT_DIRS, " +
			"switch project or reindex the active root so the graph matches disk.",
	}
}

func writeEnvelopeStatus(w http.ResponseWriter, status int, env apiEnvelope) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(env)
}

func (h *apiH) getFile(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("path")
	if h.projectDir == "" {
		writeEnvelopeStatus(w, http.StatusNotFound,
			failEnvelope("File not found '"+raw+"': the engine has no active project directory"))
		return
	}
	clean := cleanProjectRelativePath(raw, h.projectDir)
	extras := projectDirsFromEnv()

	resolved, ferr := resolveReadableFile(clean, h.projectDir, extras)
	if ferr != nil {
		writeEnvelopeStatus(w, ferr.status, failEnvelope(ferr.msg))
		return
	}

	f, err := os.Open(resolved)
	if err != nil {
		writeEnvelopeStatus(w, http.StatusInternalServerError,
			failEnvelope("Failed to read file '"+clean+"': "+err.Error()))
		return
	}
	defer f.Close()
	content, err := io.ReadAll(io.LimitReader(f, maxFileBytes))
	if err != nil {
		writeEnvelopeStatus(w, http.StatusInternalServerError,
			failEnvelope("Failed to read file '"+clean+"': "+err.Error()))
		return
	}
	writeEnvelopeStatus(w, http.StatusOK, okEnvelope(map[string]any{"content": string(content)}))
}
