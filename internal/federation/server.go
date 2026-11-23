// The server side of #372: the two routes the client half of this package
// pushes to and pulls from. Rust shipped that client against a shared server no
// LeanKG codebase ever implemented, so the contract here is derived from the
// client's own payload (pushPayload) plus the apply policy a partial sync
// needs — see the package comment for the full set of rules.
//
//	POST /api/v2/graph/push  Contributor+  upsert the received graph
//	GET  /api/v2/graph       any valid role  publish the local graph
//
// Auth is self-contained. The process-wide gate in internal/auth classifies
// writes from a prefix list that does not name these paths, so a Viewer bearer
// would pass straight through to a write; the receiver resolves the caller
// itself and requires Contributor+ to mutate. Bearer comes first, and Rust's
// X-LeanKG-Token header is accepted as a fallback for pre-bearer clients.

package federation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/httpx"
	"github.com/FreePeak/LeanKG/internal/store"
)

// Provenance metadata keys stamped on every row a push or pull applies.
const (
	MetaSource   = "federation_source"
	MetaEnv      = "federation_env"
	MetaPushedAt = "federation_pushed_at"
)

// GraphPath is the read route a pull consumes: the mirror of PushPath, same
// envelope in the opposite direction.
const GraphPath = "/api/v2/graph"

// badPayload marks a rejection caused by the caller's own document, so the
// handler can answer 400 while a store or transport fault answers 500.
type badPayload struct{ err error }

func (b *badPayload) Error() string { return b.err.Error() }
func (b *badPayload) Unwrap() error { return b.err }

// Register mounts both federation routes on a mux, so a server adds one line
// instead of re-deriving the method/path pairing the receiver serves.
// Patterns are exact: /api/v2/graph/push and /api/v2/graph are different
// documents, and a Go mux matches a bare pattern literally.
func Register(mux *http.ServeMux, fed http.Handler) {
	mux.Handle("POST "+PushPath, auth.Require(auth.Contributor, fed))
	mux.Handle("GET "+GraphPath, auth.Require(auth.Viewer, fed))
}

// receiverStore is what a receiver needs: the graph surface plus the token and
// identity tables the caller's role resolves against.
type receiverStore interface {
	graphStore
	auth.Store
}

// Receiver serves one project's graph over the federation protocol.
func Receiver(st receiverStore, projectDir string) http.Handler {
	r := &receiver{st: st, service: serviceName(projectDir)}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == PushPath:
			r.push(w, req)
		case req.Method == http.MethodGet && req.URL.Path == GraphPath:
			r.publish(w, req)
		default:
			httpx.WriteMessage(w, http.StatusMethodNotAllowed, fmt.Sprintf(
				"federation: %s %s is not a graph route (expected POST %s or GET %s)",
				req.Method, req.URL.Path, PushPath, GraphPath))
		}
	})
}

type receiver struct {
	st      receiverStore
	service string
}

// push applies the received graph and answers with the envelope plus the
// transfer's own counts.
func (h *receiver) push(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	role, err := h.callerRole(req)
	if err != nil {
		httpx.WriteMessage(w, http.StatusUnauthorized, err.Error())
		return
	}
	if role < auth.Contributor {
		httpx.WriteMessage(w, http.StatusForbidden, fmt.Sprintf(
			"federation: pushing needs a Contributor token, this caller is %s", role))
		return
	}

	var payload pushPayload
	if err := httpx.DecodeJSON(w, req, &payload); err != nil {
		httpx.WriteError(w, err)
		return
	}
	now := time.Now().UTC()
	elements, relationships, err := applyGraph(ctx, h.st, payload, now)
	if err != nil {
		var bad *badPayload
		if errors.As(err, &bad) {
			httpx.WriteMessage(w, http.StatusBadRequest, err.Error())
		} else {
			httpx.WriteError(w, store.Fail("push", err))
		}
		return
	}
	httpx.WriteJSON(w, http.StatusOK, graphDocument{
		pushPayload: pushPayload{
			Env:           payload.Env,
			Service:       payload.Service,
			Elements:      wireElements(nil),
			Relationships: wireRelationships(nil),
		},
		AppliedElements:      elements,
		AppliedRelationships: relationships,
		PushedAt:             now.Format(time.RFC3339),
	})
}

// publish serves the local graph for a pull. Reads need no role floor: any
// caller the token store resolves (including the open local default) may read
// a graph it could already query through /api/v1/query.
func (h *receiver) publish(w http.ResponseWriter, req *http.Request) {
	if _, err := h.callerRole(req); err != nil {
		httpx.WriteMessage(w, http.StatusUnauthorized, err.Error())
		return
	}
	elements, err := h.st.Elements(req.Context())
	if err != nil {
		httpx.WriteMessage(w, http.StatusInternalServerError, fmt.Errorf("federation: read elements: %w", err).Error())
		return
	}
	relationships, err := readRelationships(req.Context(), h.st)
	if err != nil {
		httpx.WriteMessage(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now().UTC()
	httpx.WriteJSON(w, http.StatusOK, graphDocument{
		pushPayload: pushPayload{
			// The store has no env column, so the published envelope reports the
			// environment the caller asked about, falling back to "local".
			Env:           firstNonEmpty(req.URL.Query().Get("env"), req.Header.Get(HeaderEnv), localEnv),
			Service:       h.service,
			Elements:      wireElements(elements),
			Relationships: wireRelationships(relationships),
		},
		AppliedElements:      len(elements),
		AppliedRelationships: len(relationships),
		PushedAt:             now.Format(time.RFC3339),
	})
}

// callerRole resolves the request against the token store. A bearer is
// preferred; Rust-era clients that only send X-LeanKG-Token are lifted into one
// so the same token table decides them.
func (h *receiver) callerRole(req *http.Request) (auth.Role, error) {
	if req.Header.Get("Authorization") == "" {
		if token := strings.TrimSpace(req.Header.Get(HeaderToken)); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	return auth.EffectiveRole(h.st, req)
}

// graphDocument is the federation response: the graph itself, plus what this
// exchange moved and when. The extra keys are additive, so a client decoding
// pushPayload ignores them and one decoder handles every reply.
type graphDocument struct {
	pushPayload
	AppliedElements      int    `json:"applied_elements"`
	AppliedRelationships int    `json:"applied_relationships"`
	PushedAt             string `json:"pushed_at"`
}

// applyGraph upserts a received graph and reports the rows that landed. Push
// and pull both apply through here, so neither direction can disagree about
// what "synced" means.
//
// Two store transactions (elements, then relationships) rather than one: the
// store contract exposes no cross-table transaction, and both writes are
// idempotent upserts, so re-running a sync that failed between them converges.
func applyGraph(ctx context.Context, st graphStore, payload pushPayload, at time.Time) (int, int, error) {
	if err := validateGraph(payload); err != nil {
		return 0, 0, err
	}

	if len(payload.Elements) > 0 {
		// Content is not on the wire (wireElement has no content slot, mirroring
		// Rust's CodeElement), so carry forward whatever the local store holds
		// per qualified name: a sync updates structure without erasing the text
		// the FTS index reads. Qualified names the document introduces land
		// contentless, which is what an index rebuild fills in.
		//
		// ponytail: reads the whole local graph per sync that carries elements.
		// Upgrade path is a qualified-name-batched read, or a content-preserving
		// upsert in internal/store — both outside this package's ownership.
		content, err := localContent(ctx, st)
		if err != nil {
			return 0, 0, err
		}
		elements := make([]store.Element, 0, len(payload.Elements))
		for _, in := range payload.Elements {
			element := store.Element{
				QualifiedName: in.QualifiedName,
				ElementType:   in.ElementType,
				Name:          in.Name,
				FilePath:      in.FilePath,
				LineStart:     in.LineStart,
				LineEnd:       in.LineEnd,
				Language:      in.Language,
				Content:       content[in.QualifiedName],
				Metadata:      provenance(in.Metadata, payload, at),
			}
			if in.ParentQualified != nil {
				element.ParentQualified = *in.ParentQualified
			}
			elements = append(elements, element)
		}
		if err := st.UpsertElements(ctx, elements); err != nil {
			return 0, 0, fmt.Errorf("federation: apply elements: %w", err)
		}
	}

	if len(payload.Relationships) > 0 {
		relationships := make([]store.Relationship, 0, len(payload.Relationships))
		for _, in := range payload.Relationships {
			relationships = append(relationships, store.Relationship{
				Source:     in.SourceQualified,
				Target:     in.TargetQualified,
				RelType:    in.RelType,
				Confidence: in.Confidence,
				Metadata:   provenance(in.Metadata, payload, at),
			})
		}
		if err := st.UpsertRelationships(ctx, relationships); err != nil {
			return 0, 0, fmt.Errorf("federation: apply relationships: %w", err)
		}
	}

	// The counts describe the document, not the receiver's total: a partial
	// sync cannot claim to know the whole graph.
	return len(payload.Elements), len(payload.Relationships), nil
}

// validateGraph rejects rows the store could not address afterwards. It runs
// over the whole document before any write, so one malformed relationship
// cannot leave a push half-applied.
func validateGraph(payload pushPayload) error {
	for _, in := range payload.Elements {
		if strings.TrimSpace(in.QualifiedName) == "" {
			return &badPayload{errors.New("federation: pushed element has no qualified_name")}
		}
	}
	for _, in := range payload.Relationships {
		if strings.TrimSpace(in.SourceQualified) == "" || strings.TrimSpace(in.TargetQualified) == "" ||
			strings.TrimSpace(in.RelType) == "" {
			return &badPayload{errors.New("federation: pushed relationship is missing source, target or type")}
		}
	}
	return nil
}

// localContent maps every qualified name in the store to its element content.
func localContent(ctx context.Context, st graphStore) (map[string]string, error) {
	existing, err := st.Elements(ctx)
	if err != nil {
		return nil, fmt.Errorf("federation: read local elements: %w", err)
	}
	out := make(map[string]string, len(existing))
	for _, e := range existing {
		out[e.QualifiedName] = e.Content
	}
	return out, nil
}

// provenance merges the transfer's identity into a row's metadata. The arriving
// push owns the three provenance keys — recording which service wrote the row
// last is the whole point — and everything else the row carried survives.
func provenance(meta map[string]any, payload pushPayload, at time.Time) map[string]any {
	out := make(map[string]any, len(meta)+3)
	for k, v := range meta {
		out[k] = v
	}
	out[MetaSource] = firstNonEmpty(strings.TrimSpace(payload.Service), "unknown")
	out[MetaEnv] = firstNonEmpty(strings.TrimSpace(payload.Env), localEnv)
	out[MetaPushedAt] = at.Format(time.RFC3339)
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
