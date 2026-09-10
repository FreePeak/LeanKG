package embed

import (
	"fmt"
	"os"
	"strconv"
)

// FromEnv builds a Provider from the LEANKG_EMBED_* environment:
//
//	LEANKG_EMBED_PROVIDER  local | openai | deterministic (default local)
//	LEANKG_EMBED_BASE_URL  sidecar/API base URL (default http://127.0.0.1:8080/v1)
//	LEANKG_EMBED_API_KEY   bearer token; omitted from requests when empty
//	LEANKG_EMBED_MODEL     model id (required for openai)
//	LEANKG_EMBED_DIMS      vector dimensions (default 384, the minilm sidecar)
//	LEANKG_EMBED_REVISION  pinned model revision (default "openai:<model>",
//	                       so a model switch invalidates the stamp)
func FromEnv() (Provider, error) {
	name := os.Getenv("LEANKG_EMBED_PROVIDER")
	if name == "" {
		name = "local"
	}
	dims := 384
	if v := os.Getenv("LEANKG_EMBED_DIMS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("embed: invalid LEANKG_EMBED_DIMS %q", v)
		}
		dims = n
	}

	switch name {
	case "deterministic":
		return Deterministic(dims), nil
	case "local", "openai":
		model := os.Getenv("LEANKG_EMBED_MODEL")
		if name == "openai" && model == "" {
			return nil, fmt.Errorf("embed: LEANKG_EMBED_MODEL is required for LEANKG_EMBED_PROVIDER=openai")
		}
		revision := os.Getenv("LEANKG_EMBED_REVISION")
		if revision == "" {
			revision = "openai:" + model
		}
		baseURL := os.Getenv("LEANKG_EMBED_BASE_URL")
		if baseURL == "" {
			baseURL = "http://127.0.0.1:8080/v1"
		}
		return OpenAICompatible(baseURL, os.Getenv("LEANKG_EMBED_API_KEY"), model, dims, revision), nil
	default:
		return nil, fmt.Errorf("embed: unknown LEANKG_EMBED_PROVIDER %q (want local|openai|deterministic)", name)
	}
}
