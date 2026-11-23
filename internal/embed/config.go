package embed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Provider names. The provider-name switch is build; the policy checks below
// compare against these constants rather than switching again.
const (
	ProviderLocal         = "local"
	ProviderOpenAI        = "openai"
	ProviderDeterministic = "deterministic"
)

// Config is the explicit embed configuration. cmd/ fills it from the
// LEANKG_EMBED_* environment (cmd/internal/embedenv); nothing under internal/
// reads the environment. The zero value is the default local provider.
type Config struct {
	Provider       string        // local | openai | deterministic; "" means local
	Model          string        // model id or catalog key (required for openai)
	BaseURL        string        // OpenAI-compatible API root
	APIKey         string        // bearer token; omitted from requests when empty
	Dims           int           // vector width; 0 = the model's own width
	Revision       string        // explicit revision pin; "" = catalog pin or probed
	TextCap        int           // operator write-cap pin in runes; 0 = none
	RequestTimeout time.Duration // embeddings request timeout; 0 = defaultRequestTimeout
	Sidecar        SidecarConfig // spawn settings for provider=local without BaseURL
}

// defaultDims is the historical local-family (BGE-small / MiniLM) width, used
// when neither the config nor the catalog pins one.
const defaultDims = 384

func (c Config) providerName() string {
	if c.Provider == "" {
		return ProviderLocal
	}
	return c.Provider
}

func (c Config) timeout() time.Duration {
	if c.RequestTimeout > 0 {
		return c.RequestTimeout
	}
	return defaultRequestTimeout
}

// dims resolves the widths for a model. native is the width the provider's own
// wire returns: the catalog pin, or Config.Dims / fallback for an uncatalogued
// model. reported is the width the provider exposes: native, or the Matryoshka
// truncation Config.Dims selects from the row's Widths.
func (c Config) dims(model string, fallback int) (native, reported int, err error) {
	row, ok := Lookup(model)
	if !ok {
		n, err := ResolveDims(model, c.Dims, fallback)
		return n, n, err
	}
	if c.Dims == 0 {
		return row.Dims, row.Dims, nil
	}
	if !slices.Contains(row.Widths(), c.Dims) {
		return 0, 0, fmt.Errorf("embed: dims %d is not a width %s serves (allowed: %s)",
			c.Dims, model, widthList(row.Widths()))
	}
	return row.Dims, c.Dims, nil
}

// widthList renders widths for an error message: "768, 512, 256, 128".
func widthList(widths []int) string {
	parts := make([]string, len(widths))
	for i, w := range widths {
		parts[i] = strconv.Itoa(w)
	}
	return strings.Join(parts, ", ")
}

// localModelID is the model id the local provider stamps: Config.Model, or
// "local" when unset. llama-server loads exactly one GGUF; the name only feeds
// the stamp, so it must not be empty.
func (c Config) localModelID() string {
	if c.Model != "" {
		return c.Model
	}
	return "local"
}

// localPinned reports whether the local provider's revision comes from a pin
// (an explicit Revision, or a catalogued model) rather than from the GGUF a
// running sidecar reports. Only a pinned revision is known without a probe.
func (c Config) localPinned() bool {
	_, inCatalog := Lookup(c.localModelID())
	return c.Revision != "" || inCatalog
}

// Start builds the provider cfg names. For provider=local with no BaseURL it
// spawns the llama.cpp sidecar and attaches to it; release shuts the sidecar
// down and is a no-op otherwise. Context cancel also shuts the sidecar down.
func Start(ctx context.Context, cfg Config) (Provider, func(), error) {
	noop := func() {}
	if cfg.providerName() != ProviderLocal || cfg.BaseURL != "" {
		p, _, err := build(cfg)
		return p, noop, err
	}
	// A width the model does not serve is a configuration error: reject it
	// before a sidecar process exists, not after one has started and died.
	if _, _, err := cfg.dims(cfg.localModelID(), defaultDims); err != nil {
		return nil, noop, err
	}
	sidecar := cfg.Sidecar
	if sidecar.Args == nil {
		sidecar.Args = DefaultSidecarArgs("")
	}
	sc, err := StartSidecar(ctx, sidecar)
	if err != nil {
		return nil, noop, err
	}
	attached := cfg
	attached.BaseURL = sc.BaseURL()
	p, _, err := build(attached)
	if err != nil {
		_ = sc.Shutdown()
		return nil, noop, err
	}
	return p, func() { _ = sc.Shutdown() }, nil
}

// Attach builds the provider cfg names without spawning a process: a local
// provider must already be running at cfg.BaseURL. A missing sidecar is an
// error, never fake vectors. The release function is a no-op.
func Attach(cfg Config) (Provider, func(), error) {
	noop := func() {}
	if cfg.providerName() == ProviderLocal && cfg.BaseURL == "" {
		return nil, noop, errors.New("embed: provider=local needs a running llama.cpp sidecar; " +
			"set BaseURL, or use embed.Start to spawn one")
	}
	p, _, err := build(cfg)
	return p, noop, err
}

// AttachHook is the provider policy of `leankg index --hook` (the SessionStart
// hook). It never spawns a process and never calls a remote endpoint: only the
// in-process deterministic provider is allowed, and a local provider once it is
// attached to a loopback BaseURL.
func AttachHook(cfg Config) (Provider, func(), error) {
	noop := func() {}
	switch name := cfg.providerName(); {
	case name == ProviderDeterministic:
	case name == ProviderLocal:
		if cfg.BaseURL == "" {
			return nil, noop, hookRefused("BaseURL is unset, so a sidecar would have to be spawned")
		}
		if !isLoopbackURL(cfg.BaseURL) {
			return nil, noop, hookRefused(fmt.Sprintf("BaseURL %s is not a loopback address", cfg.BaseURL))
		}
	default:
		return nil, noop, hookRefused(fmt.Sprintf("provider %q is not local or deterministic", name))
	}
	return Attach(cfg)
}

// hookRefused is the error AttachHook returns for a provider the hook may not use.
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

// build is the one provider-name switch: it turns a Config into a Provider,
// and the native width the provider's own wire returns. It spawns nothing and
// does no I/O.
func build(cfg Config) (Provider, int, error) {
	switch name := cfg.providerName(); name {
	case ProviderDeterministic:
		native, width, err := cfg.dims(ProviderDeterministic, defaultDims)
		if err != nil {
			return nil, 0, err
		}
		return Deterministic(width), native, nil
	case ProviderOpenAI:
		return openAIProvider(cfg)
	case ProviderLocal:
		return newLocalProvider(cfg)
	default:
		return nil, 0, fmt.Errorf("embed: unknown provider %q (want local|openai|deterministic)", name)
	}
}

// openAIProvider builds the remote OpenAI-compatible provider: Config.Model is
// required; base URL and revision come from the catalog when the model is
// pinned there (see the precedence note in catalog.go).
func openAIProvider(cfg Config) (Provider, int, error) {
	if cfg.Model == "" {
		return nil, 0, fmt.Errorf("embed: a model is required for provider %s (LEANKG_EMBED_MODEL)", ProviderOpenAI)
	}
	native, width, err := cfg.dims(cfg.Model, defaultDims)
	if err != nil {
		return nil, 0, err
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = EndpointFor(cfg.Model)
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	inner := newOpenAICompatible(baseURL, cfg.APIKey, cfg.Model, native,
		Pin(cfg.Model, ProviderOpenAI, cfg.Revision), cfg.timeout(), ProviderOpenAI, cfg.TextCap)
	return withWidth(inner, native, width), native, nil
}

func newLocalProvider(cfg Config) (Provider, int, error) {
	model := cfg.localModelID()
	native, width, err := cfg.dims(model, defaultDims)
	if err != nil {
		return nil, 0, err
	}
	inner := &localProvider{
		openaiCompatible: newOpenAICompatible(cfg.BaseURL, cfg.APIKey, model, native,
			Pin(model, ProviderLocal, cfg.Revision), cfg.timeout(), ProviderLocal, cfg.TextCap),
		pinned:   cfg.localPinned(),
		probeSem: make(chan struct{}, 1),
	}
	return withWidth(inner, native, width), native, nil
}

// localProvider is the sidecar-backed provider. It reuses the OpenAI-compatible
// wire client under the "local" transport label.
//
// The revision is NOT taken from a catalog artifact pin unless the operator
// named a catalog model: a llama.cpp sidecar serves whatever GGUF the operator
// pointed it at, and claiming an HF commit for weights LeanKG did not fetch
// would make the stamp a lie. When nothing is pinned, Probe asks the sidecar
// which GGUF it serves (GET /props) and the revision is derived from that.
type localProvider struct {
	*openaiCompatible
	pinned bool

	probeSem chan struct{} // one slot: the holder reads /props; a waiter gives up when its ctx ends
	mu       sync.Mutex
	probed   bool      // a /props answer was read
	fpRev    string    // "local:<gguf>[@<commit>]" from the answer
	fpCtx    int       // the served context window from the answer
	nextTry  time.Time // earliest next /props attempt after a failure
}

// propsRetry spaces /props attempts after a failure, so a sidecar that comes up
// later is still identified without a restart, and a dead one is not hammered.
const propsRetry = 10 * time.Second

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

// Probe asks the sidecar which model it serves, once. The answer is cached on
// success; a failure is retried at most every propsRetry, and a probe cut short
// by ctx says nothing and is retried on the next call. It is the only place the
// local provider does I/O for its identity.
func (l *localProvider) Probe(ctx context.Context) error {
	select {
	case l.probeSem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-l.probeSem }()
	l.mu.Lock()
	skip := l.probed || time.Now().Before(l.nextTry)
	l.mu.Unlock()
	if skip {
		return nil
	}

	rev, nctx, ok := l.fetchProps(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if ok {
		l.probed, l.fpRev, l.fpCtx = true, rev, nctx
	} else {
		l.nextTry = time.Now().Add(propsRetry)
	}
	return nil
}

// fetchProps reads the sidecar's /props. ok is false when the endpoint is not a
// llama.cpp sidecar or its answer names no model.
func (l *localProvider) fetchProps(ctx context.Context) (rev string, nctx int, ok bool) {
	root := strings.TrimSuffix(strings.TrimSuffix(l.baseURL, "/"), "/v1")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, root+"/props", nil)
	if err != nil {
		return "", 0, false
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
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
	return rev, nctx, true
}

// Identity is the wrapped identity plus what Probe learned. Before a successful
// probe an unpinned revision is the legacy "local:<model>" fallback and the
// budget is unknown (0). It never does I/O.
func (l *localProvider) Identity() Identity {
	id := l.openaiCompatible.Identity()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.probed {
		if !l.pinned {
			id.Revision = l.fpRev
		}
		id.TextBudget = budgetFor(l.fpCtx)
	}
	return id
}

// budgetFor is the per-text rune budget for a sidecar serving a context window
// of nctx tokens: about two runes per token for dense code, minus room for the
// document header. 0 when the window is unknown.
func budgetFor(nctx int) int {
	if nctx <= 0 {
		return 0
	}
	return max(nctx*2-64, 256)
}
