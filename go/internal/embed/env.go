package embed

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
)

// FromEnv builds a Provider from the LEANKG_EMBED_* environment:
//
//	LEANKG_EMBED_PROVIDER     local | openai | deterministic (default local)
//	LEANKG_EMBED_MODEL        model id / catalog key (required for openai)
//	LEANKG_EMBED_BASE_URL     OpenAI-compatible API root
//	LEANKG_EMBED_API_KEY      bearer token; omitted from requests when empty
//	LEANKG_EMBED_DIMS         vector dimensions
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
//     sidecar family's width).
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
		dims, err := providerDims("deterministic", 0, defaultDims)
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
		dims, err := providerDims("deterministic", 0, defaultDims)
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

// providerDims resolves the vector width for a model through the catalog
// precedence: explicit LEANKG_EMBED_DIMS > catalog pin > fallback.
func providerDims(model string, explicit, fallback int) (int, error) {
	if v := os.Getenv("LEANKG_EMBED_DIMS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("embed: invalid LEANKG_EMBED_DIMS %q", v)
		}
		explicit = n
	}
	return ResolveDims(model, explicit, fallback)
}

// openAIProvider builds the remote OpenAI-compatible provider: LEANKG_EMBED_MODEL
// is required; base URL and revision come from the catalog when the model is
// pinned there (see the precedence note on FromEnv).
func openAIProvider(name, fallbackBaseURL string) (Provider, error) {
	model := os.Getenv("LEANKG_EMBED_MODEL")
	if model == "" {
		return nil, fmt.Errorf("embed: LEANKG_EMBED_MODEL is required for LEANKG_EMBED_PROVIDER=%s", name)
	}
	dims, err := providerDims(model, 0, defaultDims)
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
	return OpenAICompatible(baseURL, os.Getenv("LEANKG_EMBED_API_KEY"), model, dims, Pin(model, name)), nil
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
}

func newLocalProvider(baseURL string) (Provider, error) {
	// llama-server loads exactly one GGUF; LEANKG_EMBED_MODEL may name it
	// for the stamp but must not be empty (the stamp needs a model id).
	model := os.Getenv("LEANKG_EMBED_MODEL")
	if model == "" {
		model = "local"
	}
	dims, err := providerDims(model, 0, defaultDims)
	if err != nil {
		return nil, err
	}
	return &localProvider{&openaiCompatible{
		baseURL:  baseURL,
		apiKey:   os.Getenv("LEANKG_EMBED_API_KEY"),
		model:    model,
		dims:     dims,
		revision: Pin(model, "local"),
		client:   &http.Client{},
	}}, nil
}

func (l *localProvider) Provider() string { return "local" }
