// Package federation ports the Rust shared-server graph sync commands
// (`leankg push` / `leankg pull`; src/main.rs push_to_remote /
// pull_from_remote, rev f7624143^).
//
// The protocol is defined by the Rust client alone:
//
//	POST {remote}/api/v2/graph/push
//	  headers: X-LeanKG-Token, X-LeanKG-Engineer, X-LeanKG-Env
//	  body:    {"env":…,"service":…,"elements":[…],"relationships":[…]}
//
//	GET  {remote}/api/v2/status
//	  headers: X-LeanKG-Token, X-LeanKG-Env
//
// Deliberate port decisions, all observable in the Rust source:
//
//   - pull is a connectivity probe, not a data pull. It applies nothing
//     locally and prints only "Successfully connected to …"; the body of
//     /api/v2/status (the Rust ApiResponse envelope) is ignored.
//   - A rejected request is not an error: Rust printed "Push failed (…)" /
//     "Pull failed (…)" to stderr and still exited 0, while a transport
//     failure propagated through `?` and exited 1. PushResult.StatusResult
//     carry that distinction; Failed() is reported, not returned.
//   - Responses are never parsed, so a malformed response body cannot fail a
//     push or a pull — it only appears verbatim in the failure line.
//   - Element/relationship JSON is the Rust CodeElement/Relationship
//     serialization. cluster_id/cluster_label are always null (the Go store
//     has no cluster column, exactly as internal/export documents), metadata
//     is an empty object where the Rust row read fell back to `{}`, and the
//     per-row env is "local" — the Rust column default, which its indexer
//     never overrode; the Go store has no per-row env column.
//
// No server in this repo registers /api/v2/graph/push (the Rust Axum server did
// not either): the endpoint contract is the client's payload and headers.
package federation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Header names the Rust client set (push_to_remote / pull_from_remote).
const (
	HeaderToken    = "X-LeanKG-Token"
	HeaderEngineer = "X-LeanKG-Engineer"
	HeaderEnv      = "X-LeanKG-Env"
)

// Routes the Rust client called.
const (
	PushPath   = "/api/v2/graph/push"
	StatusPath = "/api/v2/status"
)

// maxRelationships is the engine-wide "read every relationship" cap, matching
// internal/export. The Rust push read relationships unbounded; the Go backend
// read takes a limit and treats <=0 as a 1000-row page. Push fails loudly
// rather than publishing a silently truncated graph when the cap is reached.
//
// Var, not const, only so the truncation guard is testable without a
// million-row graph.
var maxRelationships = 1 << 20

// localEnv is the env every Rust row carried out of its store: code_elements
// and relationships default to 'local' and the indexer never set the column.
// The Go store has no per-row env, so a locally indexed graph is published
// with this value.
const localEnv = "local"

// Options is one sync against a shared server. Remote/Token/Env are the Rust
// CLI flags; the zero value is not usable (Remote and Token are validated by
// the server, as in Rust, which sent them unconditionally).
type Options struct {
	// Remote is the shared server base URL (Rust --remote).
	Remote string
	// Token is the team token sent as X-LeanKG-Token (Rust --token).
	Token string
	// Env is the environment label (Rust --env).
	Env string
	// Engineer overrides X-LeanKG-Engineer. Empty falls back to $USER, and to
	// "unknown" only when $USER is unset (Rust main.rs:1706 uses
	// env::var("USER").unwrap_or_else(..), so a set-but-empty USER sends "").
	Engineer string
	// ProjectDir is the local project root. The payload's service name is its
	// basename (Rust project_path.file_name()); empty, "." or ".." — paths
	// whose Rust file_name() is None — send "unknown".
	ProjectDir string
	// Client is the HTTP client; nil uses http.DefaultClient, which like
	// reqwest::blocking::Client::new() applies no timeout (a whole-graph
	// upload must not be cut off mid-flight).
	Client *http.Client
}

// PushResult is one push attempt.
type PushResult struct {
	// Service is the payload's service name (basename of Options.ProjectDir).
	Service string
	// Elements and Relationships are what was read locally and sent.
	Elements      int
	Relationships int
	// StatusCode and Status describe the response. Status mirrors the Rust
	// "{status}" formatting, which printed the code with its reason phrase.
	StatusCode int
	Status     string
	// Body is the response body, reported verbatim on failure and exposed on
	// success for callers (the Rust command never read it on success).
	Body string

	remote string
	env    string
}

// Failed reports a non-2xx response. Rust printed those and exited 0.
func (r PushResult) Failed() bool { return r.StatusCode < 200 || r.StatusCode >= 300 }

// Message is the exact line the Rust command printed: the success summary on
// stdout, or "Push failed (…)" on stderr.
func (r PushResult) Message() string {
	if r.Failed() {
		return fmt.Sprintf("Push failed (%s): %s", r.Status, r.Body)
	}
	return fmt.Sprintf("Pushed %d elements and %d relationships to %s (env: %s)",
		r.Elements, r.Relationships, r.remote, r.env)
}

// Report writes Message where the Rust command wrote it: stdout on success,
// stderr on failure. Like Rust, a reported failure does not fail the caller —
// see Failed().
func (r PushResult) Report(stdout, stderr io.Writer) {
	if r.Failed() {
		fmt.Fprintln(stderr, r.Message())
		return
	}
	fmt.Fprintln(stdout, r.Message())
}

// StatusResult is the outcome of the pull probe.
type StatusResult struct {
	StatusCode int
	Status     string
	// Body is the remote's status document. Rust ignored it: pull reports
	// connectivity only and applies nothing locally.
	Body string

	remote string
	env    string
}

// Failed reports a non-2xx response. Rust printed those and exited 0.
func (r StatusResult) Failed() bool { return r.StatusCode < 200 || r.StatusCode >= 300 }

// Message is the exact line the Rust command printed.
func (r StatusResult) Message() string {
	if r.Failed() {
		return fmt.Sprintf("Pull failed (%s): %s", r.Status, r.Body)
	}
	return fmt.Sprintf("Successfully connected to %s (env: %s)", r.remote, r.env)
}

// Report writes Message where the Rust command wrote it: stdout on success,
// stderr on failure.
func (r StatusResult) Report(stdout, stderr io.Writer) {
	if r.Failed() {
		fmt.Fprintln(stderr, r.Message())
		return
	}
	fmt.Fprintln(stdout, r.Message())
}

// Push sends the whole local graph to {remote}/api/v2/graph/push.
//
// Only a local read, request-build, transport or response-read failure is
// returned as an error (Rust's `?`, exit 1); a rejected request comes back as
// a PushResult with Failed() set, matching the Rust command's exit 0.
func Push(ctx context.Context, st store.Backend, opts Options) (PushResult, error) {
	els, err := st.Elements()
	if err != nil {
		return PushResult{}, fmt.Errorf("federation: read local elements: %w", err)
	}
	rels, err := readRelationships(st)
	if err != nil {
		return PushResult{}, err
	}

	payload := pushPayload{
		Env:           opts.Env,
		Service:       serviceName(opts.ProjectDir),
		Elements:      wireElements(els),
		Relationships: wireRelationships(rels),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return PushResult{}, fmt.Errorf("federation: encode push payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint(opts.Remote, PushPath), bytes.NewReader(body))
	if err != nil {
		return PushResult{}, fmt.Errorf("federation: build push request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderToken, opts.Token)
	req.Header.Set(HeaderEnv, opts.Env)
	req.Header.Set(HeaderEngineer, engineer(opts.Engineer))

	resp, err := client(opts.Client).Do(req)
	if err != nil {
		return PushResult{}, fmt.Errorf("federation: push to %s: %w", opts.Remote, err)
	}
	defer resp.Body.Close()

	// Rust read the body only on failure (and propagated a read error there).
	text, rerr := io.ReadAll(resp.Body)
	res := PushResult{
		Service:       payload.Service,
		Elements:      len(els),
		Relationships: len(rels),
		StatusCode:    resp.StatusCode,
		Status:        resp.Status,
		Body:          string(text),
		remote:        opts.Remote,
		env:           opts.Env,
	}
	if rerr != nil && res.Failed() {
		return PushResult{}, fmt.Errorf("federation: read push response: %w", rerr)
	}
	return res, nil
}

// Pull probes the shared server's /api/v2/status, exactly as the Rust command
// did: it reads and writes nothing locally and prints a connectivity line. The
// status body is not inspected.
func Pull(ctx context.Context, opts Options) (StatusResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint(opts.Remote, StatusPath), nil)
	if err != nil {
		return StatusResult{}, fmt.Errorf("federation: build pull request: %w", err)
	}
	req.Header.Set(HeaderToken, opts.Token)
	req.Header.Set(HeaderEnv, opts.Env)

	resp, err := client(opts.Client).Do(req)
	if err != nil {
		return StatusResult{}, fmt.Errorf("federation: pull from %s: %w", opts.Remote, err)
	}
	defer resp.Body.Close()

	text, rerr := io.ReadAll(resp.Body)
	res := StatusResult{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Body:       string(text),
		remote:     opts.Remote,
		env:        opts.Env,
	}
	if rerr != nil && res.Failed() {
		return StatusResult{}, fmt.Errorf("federation: read pull response: %w", rerr)
	}
	return res, nil
}

// readRelationships reads every local relationship, refusing to publish a
// graph the paged read may have truncated.
func readRelationships(st store.Backend) ([]store.Relationship, error) {
	rels, err := st.RelationshipsAll(maxRelationships)
	if err != nil {
		return nil, fmt.Errorf("federation: read local relationships: %w", err)
	}
	if len(rels) == maxRelationships {
		total, err := st.RelationshipCount()
		if err != nil {
			return nil, fmt.Errorf("federation: count local relationships: %w", err)
		}
		if total > len(rels) {
			return nil, fmt.Errorf("federation: local graph has %d relationships, above the %d publish cap", total, maxRelationships)
		}
	}
	return rels, nil
}

// pushPayload is the Rust serde_json::json!({...}) envelope.
type pushPayload struct {
	Env           string             `json:"env"`
	Service       string             `json:"service"`
	Elements      []wireElement      `json:"elements"`
	Relationships []wireRelationship `json:"relationships"`
}

// wireElement mirrors Rust db::models::CodeElement serialization (field order
// included). Go's store.Element carries content the Rust wire format has no
// slot for, so it is not published.
type wireElement struct {
	QualifiedName   string         `json:"qualified_name"`
	ElementType     string         `json:"element_type"`
	Name            string         `json:"name"`
	FilePath        string         `json:"file_path"`
	LineStart       int            `json:"line_start"`
	LineEnd         int            `json:"line_end"`
	Language        string         `json:"language"`
	ParentQualified *string        `json:"parent_qualified"`
	ClusterID       *string        `json:"cluster_id"`
	ClusterLabel    *string        `json:"cluster_label"`
	Metadata        map[string]any `json:"metadata"`
	Env             string         `json:"env"`
}

// wireRelationship mirrors Rust db::models::Relationship serialization: the
// qualified-name keys, and no id (serde(skip) in Rust).
type wireRelationship struct {
	SourceQualified string         `json:"source_qualified"`
	TargetQualified string         `json:"target_qualified"`
	RelType         string         `json:"rel_type"`
	Confidence      float64        `json:"confidence"`
	Metadata        map[string]any `json:"metadata"`
	Env             string         `json:"env"`
}

func wireElements(els []store.Element) []wireElement {
	out := make([]wireElement, 0, len(els))
	for _, e := range els {
		out = append(out, wireElement{
			QualifiedName:   e.QualifiedName,
			ElementType:     e.ElementType,
			Name:            e.Name,
			FilePath:        e.FilePath,
			LineStart:       e.LineStart,
			LineEnd:         e.LineEnd,
			Language:        e.Language,
			ParentQualified: optional(e.ParentQualified),
			Metadata:        metadataObject(e.Metadata),
			Env:             localEnv,
		})
	}
	return out
}

func wireRelationships(rels []store.Relationship) []wireRelationship {
	out := make([]wireRelationship, 0, len(rels))
	for _, r := range rels {
		out = append(out, wireRelationship{
			SourceQualified: r.Source,
			TargetQualified: r.Target,
			RelType:         r.RelType,
			Confidence:      r.Confidence,
			Metadata:        metadataObject(r.Metadata),
			Env:             localEnv,
		})
	}
	return out
}

// optional maps Go's empty string to Rust's None (JSON null).
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// metadataObject renders empty metadata as `{}`: the Rust row readers fell
// back to serde_json::json!({}) whenever the column was empty or unparsable.
func metadataObject(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// serviceName is Rust project_path.file_name(): the basename, or "unknown"
// when the path has no file name ("", ".", ".." and "/").
func serviceName(dir string) string {
	trimmed := strings.TrimRight(dir, "/")
	if trimmed == "" {
		return "unknown"
	}
	base := filepath.Base(trimmed)
	if base == "" || base == "." || base == ".." || base == "/" {
		return "unknown"
	}
	return base
}

// engineer is Rust's X-LeanKG-Engineer default: $USER, or "unknown" when the
// variable is absent.
func engineer(override string) string {
	if override != "" {
		return override
	}
	if user, ok := os.LookupEnv("USER"); ok {
		return user
	}
	return "unknown"
}

// endpoint builds the request URL the way Rust did: every trailing slash of
// the remote is trimmed before the path is appended.
func endpoint(remote, path string) string {
	return strings.TrimRight(remote, "/") + path
}

func client(c *http.Client) *http.Client {
	if c == nil {
		return http.DefaultClient
	}
	return c
}
