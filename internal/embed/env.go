package embed

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// FromEnv builds a Provider from the LEANKG_EMBED_* environment:
//
//	LEANKG_EMBED_PROVIDER     local | openai | deterministic (default local)
//	LEANKG_EMBED_MODEL        model id / catalog key (required for openai)
//	LEANKG_EMBED_BASE_URL     OpenAI-compatible API root
//	LEANKG_EMBED_API_KEY      bearer token; omitted from requests when empty
//	LEANKG_EMBED_DIMS         vector dimensions (a catalog row accepts its native
//	                          width or a Matryoshka truncation; anything else fails)
//	LEANKG_EMBED_REVISION     pinned model revision
//
// Catalog precedence (issue #279: the stamp's identity must come from the
// pinned catalog, not from free-form environment, or a "revision" can drift
// without meaning anything):
//
//   - Revision: LEANKG_EMBED_REVISION (explicit operator pin) > the catalog
//     pin for the selected model > "<provider>:<model>" for a model the
//     catalog does not know.
//   - Dimensions: LEANKG_EMBED_DIMS > the catalog pin > 384 (the local
//     sidecar family's width). For a catalogued model LEANKG_EMBED_DIMS must be
//     the native width or one of the row's Matryoshka widths, else the
//     constructor fails; a truncated width is served by withWidth.
//   - Base URL: LEANKG_EMBED_BASE_URL > the catalog Endpoint > the public
//     OpenAI root. Prefixes are catalog-only on purpose (Prefixes): they are
//     model-card data, not configuration, and the stamp records them.
//
// FromEnv is the ATTACH constructor: it never spawns a process. For
// provider=local it requires LEANKG_EMBED_BASE_URL pointing at an
// already-running sidecar and fails with an actionable error otherwise —
// a missing sidecar must never silently produce fake vectors (ported from
// .rustref/src/embeddings/provider.rs create_local_provider). Use
// StartProvider for the full spawn lifecycle.
func FromEnv() (Provider, error) {
	name, err := providerName()
	if err != nil {
		return nil, err
	}
	switch name {
	case "deterministic":
		dims, _, err := providerDims("deterministic", defaultDims)
		if err != nil {
			return nil, err
		}
		return Deterministic(dims), nil
	case "openai":
		return openAIProvider("openai", "https://api.openai.com/v1")
	case "local":
		baseURL := os.Getenv("LEANKG_EMBED_BASE_URL")
		if baseURL == "" {
			return nil, fmt.Errorf("embed: LEANKG_EMBED_PROVIDER=local needs a running llama.cpp sidecar; " +
				"set LEANKG_EMBED_BASE_URL, or use embed.StartProvider to spawn one " +
				"(LEANKG_EMBED_SIDECAR_CMD, default llama-server)")
		}
		p, err := newLocalProvider(baseURL)
		return p, err
	default:
		return nil, fmt.Errorf("embed: unknown LEANKG_EMBED_PROVIDER %q (want local|openai|deterministic)", name)
	}
}

// StartProvider builds the provider for the LEANKG_EMBED_* environment,
// spawning the llama.cpp sidecar when LEANKG_EMBED_PROVIDER=local and
// LEANKG_EMBED_BASE_URL is unset (see SidecarConfigFromEnv for the sidecar
// environment). With BASE_URL set it attaches to that endpoint instead.
// The returned release function shuts the sidecar down (process-group kill)
// and is a no-op for providers that need no process; context cancel also
// shuts the sidecar down. Callers must invoke release exactly like they
// defer a Close.
func StartProvider(ctx context.Context) (Provider, func(), error) {
	noop := func() {}
	name, err := providerName()
	if err != nil {
		return nil, noop, err
	}
	switch name {
	case "deterministic":
		dims, _, err := providerDims("deterministic", defaultDims)
		if err != nil {
			return nil, noop, err
		}
		return Deterministic(dims), noop, nil
	case "openai":
		p, err := openAIProvider("openai", "https://api.openai.com/v1")
		return p, noop, err
	case "local":
		if baseURL := os.Getenv("LEANKG_EMBED_BASE_URL"); baseURL != "" {
			p, err := newLocalProvider(baseURL)
			return p, noop, err
		}
		// A width the model does not serve is a configuration error: reject it
		// before a sidecar process exists, not after one has started and died.
		if _, _, err := providerDims(localModel(), defaultDims); err != nil {
			return nil, noop, err
		}
		cfg, err := SidecarConfigFromEnv()
		if err != nil {
			return nil, noop, err
		}
		sc, err := StartSidecar(ctx, cfg)
		if err != nil {
			return nil, noop, err
		}
		p, err := newLocalProvider(sc.BaseURL())
		if err != nil {
			_ = sc.Shutdown()
			return nil, noop, err
		}
		return p, func() { _ = sc.Shutdown() }, nil
	default:
		return nil, noop, fmt.Errorf("embed: unknown LEANKG_EMBED_PROVIDER %q (want local|openai|deterministic)", name)
	}
}

// AttachHookProvider is the provider policy of `leankg index --hook` (the
// SessionStart hook). It has StartProvider's signature so the embed stage takes
// either, but it never spawns a process and never calls a remote endpoint: only
// the in-process deterministic provider is allowed here, and a local provider
// once it is attached to a loopback LEANKG_EMBED_BASE_URL. Anything else returns
// an error that names the reason.
func AttachHookProvider(ctx context.Context) (Provider, func(), error) {
	noop := func() {}
	name, err := providerName()
	if err != nil {
		return nil, noop, err
	}
	switch name {
	case "deterministic":
	case "local":
		baseURL := os.Getenv("LEANKG_EMBED_BASE_URL")
		if baseURL == "" {
			return nil, noop, hookRefused("LEANKG_EMBED_BASE_URL is unset, so a sidecar would have to be spawned")
		}
		if !isLoopbackURL(baseURL) {
			return nil, noop, hookRefused(fmt.Sprintf("LEANKG_EMBED_BASE_URL %s is not a loopback address", baseURL))
		}
	default:
		return nil, noop, hookRefused(fmt.Sprintf("provider %q is not local or deterministic", name))
	}
	p, err := FromEnv()
	return p, noop, err
}

// hookRefused is the error AttachHookProvider returns for a provider the hook
// may not use.
func hookRefused(reason string) error {
	return fmt.Errorf("hook mode only attaches to a running local provider (%s)", reason)
}

// isLoopbackURL reports whether raw names a loopback host: localhost, or an IP
// in 127.0.0.0/8 or ::1.
func isLoopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// defaultDims is the historical local-family (BGE-small / MiniLM) width, used
// when neither the environment nor the catalog pins one.
const defaultDims = 384

// providerName reads LEANKG_EMBED_PROVIDER (default local).
func providerName() (string, error) {
	name := os.Getenv("LEANKG_EMBED_PROVIDER")
	if name == "" {
		name = "local"
	}
	return name, nil
}

// envDims reads LEANKG_EMBED_DIMS; 0 when unset.
func envDims() (int, error) {
	v := os.Getenv("LEANKG_EMBED_DIMS")
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("embed: invalid LEANKG_EMBED_DIMS %q", v)
	}
	return n, nil
}

// providerDims resolves the widths for a model. native is the width the
// provider's own wire returns: the catalog pin, or LEANKG_EMBED_DIMS / fallback
// for an uncatalogued model. reported is the width the provider exposes: native,
// or the Matryoshka truncation LEANKG_EMBED_DIMS selects from the row's Widths.
func providerDims(model string, fallback int) (native, reported int, err error) {
	explicit, err := envDims()
	if err != nil {
		return 0, 0, err
	}
	row, ok := Lookup(model)
	if !ok {
		n, err := ResolveDims(model, explicit, fallback)
		return n, n, err
	}
	if explicit == 0 {
		return row.Dims, row.Dims, nil
	}
	if !slices.Contains(row.Widths(), explicit) {
		return 0, 0, fmt.Errorf("embed: LEANKG_EMBED_DIMS=%d is not a width %s serves (allowed: %s)",
			explicit, model, widthList(row.Widths()))
	}
	return row.Dims, explicit, nil
}

// widthList renders widths for an error message: "768, 512, 256, 128".
func widthList(widths []int) string {
	parts := make([]string, len(widths))
	for i, w := range widths {
		parts[i] = strconv.Itoa(w)
	}
	return strings.Join(parts, ", ")
}

// openAIProvider builds the remote OpenAI-compatible provider: LEANKG_EMBED_MODEL
// is required; base URL and revision come from the catalog when the model is
// pinned there (see the precedence note on FromEnv).
func openAIProvider(name, fallbackBaseURL string) (Provider, error) {
	model := os.Getenv("LEANKG_EMBED_MODEL")
	if model == "" {
		return nil, fmt.Errorf("embed: LEANKG_EMBED_MODEL is required for LEANKG_EMBED_PROVIDER=%s", name)
	}
	native, width, err := providerDims(model, defaultDims)
	if err != nil {
		return nil, err
	}
	baseURL := os.Getenv("LEANKG_EMBED_BASE_URL")
	if baseURL == "" {
		baseURL = EndpointFor(model)
	}
	if baseURL == "" {
		baseURL = fallbackBaseURL
	}
	inner := OpenAICompatible(baseURL, os.Getenv("LEANKG_EMBED_API_KEY"), model, native, Pin(model, name))
	return withWidth(inner, native, width), nil
}

// localProvider is the sidecar-backed provider. It reuses the
// OpenAI-compatible wire client but stamps Provider() as "local" so the model
// stamp records where the vectors came from.
//
// The revision is NOT taken from a catalog artifact pin unless the operator
// named a catalog model: a llama.cpp sidecar serves whatever GGUF the operator
// pointed it at, and claiming an HF commit for weights LeanKG did not fetch
// would make the stamp a lie. An operator who wants the pin sets
// LEANKG_EMBED_MODEL to the catalog key (or LEANKG_EMBED_REVISION outright).
type localProvider struct {
	*openaiCompatible
	// fingerprint state (RS-14): when the operator pinned nothing, the
	// revision is derived from the model the sidecar actually serves.
	fpMu      sync.Mutex
	fpDone    bool
	fpRev     string
	fpCtx     int
	fpNextTry time.Time
	pinned    bool
}

// props is the subset of llama-server's GET /props this provider reads.
type props struct {
	ModelPath string `json:"model_path"`
	NCtx      int    `json:"n_ctx"`
	Defaults  struct {
		NCtx int `json:"n_ctx"`
	} `json:"default_generation_settings"`
}

// snapshotRe extracts a HuggingFace snapshot commit from a cached model path
// (…/snapshots/<40-hex>/<file>.gguf).
var snapshotRe = regexp.MustCompile(`/snapshots/([0-9a-f]{40})/`)

// fingerprint asks the sidecar which model it serves. Results are cached on
// success; failures are retried at most every 10 s, so a sidecar that comes up
// after the server still gets identified without a restart.
func (l *localProvider) fingerprint() (rev string, nctx int, ok bool) {
	l.fpMu.Lock()
	defer l.fpMu.Unlock()
	if l.fpDone {
		return l.fpRev, l.fpCtx, true
	}
	if time.Now().Before(l.fpNextTry) {
		return "", 0, false
	}
	l.fpNextTry = time.Now().Add(10 * time.Second)
	root := strings.TrimSuffix(strings.TrimSuffix(l.baseURL, "/"), "/v1")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(root + "/props")
	if err != nil {
		return "", 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, false
	}
	var p props
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil || p.ModelPath == "" {
		return "", 0, false
	}
	rev = "local:" + filepath.Base(p.ModelPath)
	if m := snapshotRe.FindStringSubmatch(filepath.ToSlash(p.ModelPath)); m != nil {
		rev += "@" + m[1]
	}
	nctx = p.Defaults.NCtx
	if nctx == 0 {
		nctx = p.NCtx
	}
	l.fpDone, l.fpRev, l.fpCtx = true, rev, nctx
	return rev, nctx, true
}

// Revision is the stamp revision. An operator pin (LEANKG_EMBED_REVISION, or
// a catalog model named in LEANKG_EMBED_MODEL) wins; otherwise it names the
// GGUF the sidecar serves, so swapping the model behind the port is drift the
// stamp guard catches instead of a silent mix of vector spaces. A sidecar that
// cannot be asked (no /props: another OpenAI-compatible server) keeps the
// legacy "local:<model>" identity.
func (l *localProvider) Revision() string {
	if l.pinned {
		return l.revision
	}
	if rev, _, ok := l.fingerprint(); ok {
		return rev
	}
	return l.revision
}

// TextBudget is the per-text rune budget for this sidecar: the model's real
// context window (≈2 runes per token for dense code, minus room for the
// document header) instead of a fixed guess; 0 = unknown (callers keep the
// conservative default).
func (l *localProvider) TextBudget() int {
	if _, nctx, ok := l.fingerprint(); ok && nctx > 0 {
		return max(nctx*2-64, 256)
	}
	return 0
}

// localModel is the model id the local provider stamps: LEANKG_EMBED_MODEL, or
// "local" when unset. llama-server loads exactly one GGUF; the name only feeds
// the stamp, so it must not be empty.
func localModel() string {
	if model := os.Getenv("LEANKG_EMBED_MODEL"); model != "" {
		return model
	}
	return "local"
}

// localPinned reports whether the local provider's revision comes from a pin (the
// operator's LEANKG_EMBED_REVISION, or a catalogued model) rather than from the
// GGUF a running sidecar reports. Only a pinned revision is known without one.
func localPinned(model string) bool {
	_, inCatalog := Lookup(model)
	return os.Getenv("LEANKG_EMBED_REVISION") != "" || inCatalog
}

func newLocalProvider(baseURL string) (Provider, error) {
	model := localModel()
	native, width, err := providerDims(model, defaultDims)
	if err != nil {
		return nil, err
	}
	inner := &localProvider{
		openaiCompatible: &openaiCompatible{
			baseURL:  baseURL,
			apiKey:   os.Getenv("LEANKG_EMBED_API_KEY"),
			model:    model,
			dims:     native,
			revision: Pin(model, "local"),
			client:   &http.Client{},
		},
		pinned: localPinned(model),
	}
	return withWidth(inner, native, width), nil
}

func (l *localProvider) Provider() string { return "local" }
