// Package federation is the shared-server graph sync: `leankg push` and
// `leankg pull`, both client and server. Rust shipped only the client half
// (src/main.rs push_to_remote / pull_from_remote, rev f7624143^); its Axum
// server never registered the routes, so the protocol here is the client's
// payload plus the apply policy a partial sync needs (server.go).
//
// One document moves in both directions:
//
//	POST {remote}/api/v2/graph/push   upsert a graph into the receiver
//	  headers: Authorization: Bearer, X-LeanKG-Token, X-LeanKG-Engineer, X-LeanKG-Env
//	  body:    {"env":…,"service":…,"elements":[…],"relationships":[…]}
//
//	GET  {remote}/api/v2/graph        publish the receiver's own graph
//	  headers: Authorization: Bearer, X-LeanKG-Token, X-LeanKG-Env
//	  answer:  the same envelope, plus applied_elements/applied_relationships
//
// Both ends apply through the same applyGraph, so a push and a pull cannot
// disagree about what "synced" means: rows are upserted by identity key
// (qualified_name; source/target/rel_type), arrival order wins, rows the
// document does not mention are never deleted, and each applied row records
// MetaSource/MetaEnv/MetaPushedAt in its metadata. Provenance lives in metadata
// rather than a column, so a graph merged from several services stays
// separable with no schema change.
//
// Deliberate deviations from the Rust source:
//
//   - Pull is a data pull. It fetches /api/v2/graph and applies it locally. A
//     remote without that route (404/405 — a Rust shared server or any
//     pre-#372 build) degrades to Rust's original behavior: probe
//     /api/v2/status, print "Successfully connected to …", apply nothing, so
//     the command keeps working against an older server. A 2xx that is not a
//     graph document is a hard error instead: an older server 404s that path,
//     so a 200 there means a broken remote and reporting connectivity would
//     hide it.
//   - The client also sends Authorization: Bearer. Rust sent X-LeanKG-Token
//     alone, which the Go auth middleware does not read; the receiver accepts
//     either, so one client talks to both generations of server.
//   - A push response is parsed for nothing (Rust read it only on failure),
//     and the graph envelope carries no element content, so applying a
//     document preserves the content already stored for a qualified name
//     rather than blanking the text the FTS index reads.
//   - A rejected request is not an error: Rust printed "Push failed (…)" /
//     "Pull failed (…)" to stderr and still exited 0, while a transport
//     failure propagated through `?` and exited 1. PushResult and PullResult
//     carry that distinction; Failed() is reported, not returned.
//   - Element/relationship JSON is the Rust CodeElement/Relationship
//     serialization. cluster_id/cluster_label are always null (the Go store
//     has no cluster column, exactly as internal/export documents), metadata
//     is an empty object where the Rust row read fell back to `{}`, and the
//     per-row env is "local" — the Rust column default, which its indexer
//     never overrode; the Go store has no per-row env column.
//   - maxRelationships caps a local read that would otherwise publish a
//     silently truncated graph; the cap refuses rather than truncating.
package federation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Header names the Rust client set (push_to_remote / pull_from_remote).
const (
	HeaderToken    = "X-LeanKG-Token"
	HeaderEngineer = "X-LeanKG-Engineer"
	HeaderEnv      = "X-LeanKG-Env"
)

// Routes the Rust client called, plus GraphPath: the read route #372 added so
// pull carries a graph instead of a status line.
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

// PullResult is the outcome of one pull: a graph applied locally, or the
// connectivity probe an older server was degraded to.
type PullResult struct {
	StatusCode int
	Status     string
	// Body is the raw response document. It is carried only when nothing was
	// applied — a rejection, or the older-server probe — because a successful
	// pull's body is the whole graph and duplicating it serves nobody.
	Body string
	// Applied reports that {remote}/api/v2/graph served a graph this process
	// upserted locally. False means the pull degraded to the Rust
	// /api/v2/status connectivity probe and applied nothing.
	Applied bool
	// Elements and Relationships are the rows the pull applied.
	Elements      int
	Relationships int

	remote string
	env    string
}

// Failed reports a non-2xx response. Rust printed those and exited 0.
func (r PullResult) Failed() bool { return r.StatusCode < 200 || r.StatusCode >= 300 }

// Message is the line the command prints: Rust's exact connectivity line for a
// probe or a rejection, and an applied-graph summary for a real pull.
func (r PullResult) Message() string {
	switch {
	case r.Failed():
		return fmt.Sprintf("Pull failed (%s): %s", r.Status, r.Body)
	case !r.Applied:
		return fmt.Sprintf("Successfully connected to %s (env: %s)", r.remote, r.env)
	default:
		return fmt.Sprintf("Pulled %d elements and %d relationships from %s (env: %s)",
			r.Elements, r.Relationships, r.remote, r.env)
	}
}

// Report writes Message where the Rust command wrote it: stdout on success,
// stderr on failure.
func (r PullResult) Report(stdout, stderr io.Writer) {
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
	authHeaders(req, opts)
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

// Pull fetches the shared server's graph via GET {remote}/api/v2/graph and
// applies it locally with applyGraph — the same upsert policy a push's
// receiver uses, so one rule defines "synced" in both directions.
//
// A remote without that route (a Rust shared server, or an older LeanKG) is
// not an error: Pull degrades to the Rust pull_from_remote probe — GET
// /api/v2/status, connectivity line, nothing applied — exactly as before the
// route existed. Only a local transport fault or a malformed graph document is
// an error (Rust's `?`, exit 1); a rejected request is a PullResult with
// Failed() set, matching Rust's exit 0 on "Pull failed (…)".
func Pull(ctx context.Context, st store.Backend, opts Options) (PullResult, error) {
	if st == nil {
		return PullResult{}, errors.New("federation: pull needs a store to apply into")
	}

	text, status, err := fetchGraph(ctx, opts)
	if err != nil {
		return PullResult{}, err
	}

	switch {
	case status.StatusCode == http.StatusNotFound || status.StatusCode == http.StatusMethodNotAllowed:
		// No such route: a Rust shared server, or any pre-#372 build. Fall back
		// to the probe rather than failing an upgrade-in-place deployment.
		return probeStatus(ctx, opts)
	case status.Failed():
		// A rejection is reported, not retried: 401/500 is not "no such
		// route", and probing again would only print the second error.
		status.Body = string(text)
		return status, nil
	case !isGraphEnvelope(text):
		// 2xx without a graph is a broken remote, not an old one — an old one
		// 404s here. Reporting connectivity instead would hide the damage.
		return PullResult{}, fmt.Errorf("federation: %s answered %s with no graph document", opts.Remote, status.Status)
	}

	var payload pushPayload
	if err := json.Unmarshal(text, &payload); err != nil {
		return PullResult{}, fmt.Errorf("federation: decode graph from %s: %w", opts.Remote, err)
	}
	elements, relationships, err := applyGraph(st, payload, time.Now().UTC())
	if err != nil {
		return PullResult{}, err
	}
	status.Applied = true
	status.Elements = elements
	status.Relationships = relationships
	status.env = firstNonEmpty(payload.Env, opts.Env)
	// The graph body stays out of the result: it is the whole document, and it
	// is already in the store.
	status.Body = ""
	return status, nil
}

// fetchGraph GETs {remote}/api/v2/graph with the Rust headers plus the bearer
// token the Go auth middleware resolves. The body comes back undecoded: the
// caller sniffs it for the envelope, decodes it, and reports it verbatim only
// when the request was rejected.
func fetchGraph(ctx context.Context, opts Options) ([]byte, PullResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint(opts.Remote, GraphPath), nil)
	if err != nil {
		return nil, PullResult{}, fmt.Errorf("federation: build pull request: %w", err)
	}
	authHeaders(req, opts)

	resp, err := client(opts.Client).Do(req)
	if err != nil {
		return nil, PullResult{}, fmt.Errorf("federation: pull from %s: %w", opts.Remote, err)
	}
	defer resp.Body.Close()

	text, rerr := io.ReadAll(resp.Body)
	res := PullResult{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		remote:     opts.Remote,
		env:        opts.Env,
	}
	if rerr != nil {
		return nil, PullResult{}, fmt.Errorf("federation: read pull response: %w", rerr)
	}
	return text, res, nil
}

// probeStatus is the Rust pull_from_remote fallback: GET /api/v2/status with
// the token and env headers, connectivity line, nothing applied. It keeps
// Rust's semantics exactly, including the exit-0 report of a rejection.
func probeStatus(ctx context.Context, opts Options) (PullResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint(opts.Remote, StatusPath), nil)
	if err != nil {
		return PullResult{}, fmt.Errorf("federation: build pull request: %w", err)
	}
	authHeaders(req, opts)

	resp, err := client(opts.Client).Do(req)
	if err != nil {
		return PullResult{}, fmt.Errorf("federation: pull from %s: %w", opts.Remote, err)
	}
	defer resp.Body.Close()

	text, rerr := io.ReadAll(resp.Body)
	res := PullResult{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Body:       string(text),
		remote:     opts.Remote,
		env:        opts.Env,
	}
	if rerr != nil && res.Failed() {
		return PullResult{}, fmt.Errorf("federation: read pull response: %w", rerr)
	}
	return res, nil
}

// authHeaders sends both credentials a Go receiver accepts: the bearer the
// auth middleware resolves, and Rust's X-LeanKG-Token for shared servers that
// still gate on it alone. One client therefore talks to either generation.
func authHeaders(req *http.Request, opts Options) {
	req.Header.Set(HeaderToken, opts.Token)
	req.Header.Set(HeaderEnv, opts.Env)
	if opts.Token != "" {
		req.Header.Set("Authorization", "Bearer "+opts.Token)
	}
}

// isGraphEnvelope distinguishes a graph document from any other 2xx JSON the
// route could return. Both envelope array keys must be present; a Rust
// /api/v2/status ApiResponse ({"success":…}) has neither and is correctly
// treated as "no graph here". Presence is enough — the full decode still
// validates every row.
func isGraphEnvelope(body []byte) bool {
	var keys struct {
		Elements      *[]json.RawMessage `json:"elements"`
		Relationships *[]json.RawMessage `json:"relationships"`
	}
	return json.Unmarshal(body, &keys) == nil && keys.Elements != nil && keys.Relationships != nil
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
