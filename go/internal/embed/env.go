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
//	LEANKG_EMBED_PROVIDER  local | openai | deterministic (default local)
//	LEANKG_EMBED_BASE_URL  OpenAI-compatible API root (default http://127.0.0.1:8080/v1)
//	LEANKG_EMBED_API_KEY   bearer token; omitted from requests when empty
//	LEANKG_EMBED_MODEL     model id (required for openai)
//	LEANKG_EMBED_DIMS      vector dimensions (default 384, the minilm sidecar)
//	LEANKG_EMBED_REVISION  pinned model revision (default "openai:<model>",
//	                       so a model switch invalidates the stamp)
//
// FromEnv is the ATTACH constructor: it never spawns a process. For
// provider=local it requires LEANKG_EMBED_BASE_URL pointing at an
// already-running sidecar and fails with an actionable error otherwise —
// a missing sidecar must never silently produce fake vectors (ported from
// .rustref/src/embeddings/provider.rs create_local_provider). Use
// StartProvider for the full spawn lifecycle.
func FromEnv() (Provider, error) {
	name, dims, err := providerEnv()
	if err != nil {
		return nil, err
	}
	switch name {
	case "deterministic":
		return Deterministic(dims), nil
	case "openai":
		return openAIProvider("openai", dims)
	case "local":
		baseURL := os.Getenv("LEANKG_EMBED_BASE_URL")
		if baseURL == "" {
			return nil, fmt.Errorf("embed: LEANKG_EMBED_PROVIDER=local needs a running llama.cpp sidecar; " +
				"set LEANKG_EMBED_BASE_URL, or use embed.StartProvider to spawn one " +
				"(LEANKG_EMBED_SIDECAR_CMD, default llama-server)")
		}
		return newLocalProvider(baseURL, dims), nil
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
	name, dims, err := providerEnv()
	if err != nil {
		return nil, noop, err
	}
	switch name {
	case "deterministic":
		return Deterministic(dims), noop, nil
	case "openai":
		p, err := openAIProvider("openai", dims)
		return p, noop, err
	case "local":
		if baseURL := os.Getenv("LEANKG_EMBED_BASE_URL"); baseURL != "" {
			return newLocalProvider(baseURL, dims), noop, nil
		}
		cfg, err := SidecarConfigFromEnv()
		if err != nil {
			return nil, noop, err
		}
		sc, err := StartSidecar(ctx, cfg)
		if err != nil {
			return nil, noop, err
		}
		return newLocalProvider(sc.BaseURL(), dims), func() { _ = sc.Shutdown() }, nil
	default:
		return nil, noop, fmt.Errorf("embed: unknown LEANKG_EMBED_PROVIDER %q (want local|openai|deterministic)", name)
	}
}

// providerEnv reads the provider name (default local) and dimension count.
func providerEnv() (name string, dims int, err error) {
	name = os.Getenv("LEANKG_EMBED_PROVIDER")
	if name == "" {
		name = "local"
	}
	dims = 384
	if v := os.Getenv("LEANKG_EMBED_DIMS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return name, 0, fmt.Errorf("embed: invalid LEANKG_EMBED_DIMS %q", v)
		}
		dims = n
	}
	return name, dims, nil
}

// openAIProvider builds the remote OpenAI-compatible provider: LEANKG_EMBED_MODEL
// is required, LEANKG_EMBED_BASE_URL defaults to the public API, and the
// revision stamp defaults to openai:<model> so a model switch invalidates
// the collection.
func openAIProvider(name string, dims int) (Provider, error) {
	model := os.Getenv("LEANKG_EMBED_MODEL")
	if model == "" {
		return nil, fmt.Errorf("embed: LEANKG_EMBED_MODEL is required for LEANKG_EMBED_PROVIDER=%s", name)
	}
	revision := os.Getenv("LEANKG_EMBED_REVISION")
	if revision == "" {
		revision = "openai:" + model
	}
	baseURL := os.Getenv("LEANKG_EMBED_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return OpenAICompatible(baseURL, os.Getenv("LEANKG_EMBED_API_KEY"), model, dims, revision), nil
}

// localProvider is the sidecar-backed provider. It reuses the
// OpenAI-compatible wire client but stamps Provider() as "local" so the
// model stamp records where the vectors came from.
type localProvider struct {
	*openaiCompatible
}

func newLocalProvider(baseURL string, dims int) *localProvider {
	// llama-server loads exactly one GGUF; LEANKG_EMBED_MODEL may name it
	// for the stamp but must not be empty (the stamp needs a model id).
	model := os.Getenv("LEANKG_EMBED_MODEL")
	revision := os.Getenv("LEANKG_EMBED_REVISION")
	if model == "" {
		model = "local"
	}
	if revision == "" {
		revision = "local:" + model
	}
	return &localProvider{&openaiCompatible{
		baseURL:  baseURL,
		apiKey:   os.Getenv("LEANKG_EMBED_API_KEY"),
		model:    model,
		dims:     dims,
		revision: revision,
		client:   &http.Client{},
	}}
}

func (l *localProvider) Provider() string { return "local" }
