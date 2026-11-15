package embedenv

import (
	"reflect"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/embed"
)

func lookupFrom(env map[string]string) func(string) string {
	return func(k string) string { return env[k] }
}

// TestLoadMapsEnvironmentToConfig is the one place the LEANKG_EMBED_* variables
// become an embed.Config. Every field the operator can set is covered.
func TestLoadMapsEnvironmentToConfig(t *testing.T) {
	cfg, err := Load(lookupFrom(map[string]string{
		"LEANKG_EMBED_PROVIDER":             "openai",
		"LEANKG_EMBED_MODEL":                "text-embedding-3-small",
		"LEANKG_EMBED_BASE_URL":             "http://127.0.0.1:9/v1",
		"LEANKG_EMBED_API_KEY":              "k",
		"LEANKG_EMBED_DIMS":                 "512",
		"LEANKG_EMBED_REVISION":             "rev-1",
		"LEANKG_EMBED_TEXT_CAP":             "1000",
		"LEANKG_EMBED_REQUEST_TIMEOUT_SECS": "7",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := embed.Config{
		Provider: "openai", Model: "text-embedding-3-small", BaseURL: "http://127.0.0.1:9/v1",
		APIKey: "k", Dims: 512, Revision: "rev-1", TextCap: 1000, RequestTimeout: 7 * time.Second,
	}
	got := cfg
	got.Sidecar = embed.SidecarConfig{}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load = %+v\nwant %+v", got, want)
	}
}

// TestLoadRejectsMalformedValues keeps a typo from silently choosing a value:
// each malformed variable is an error that names it.
func TestLoadRejectsMalformedValues(t *testing.T) {
	for _, tc := range []struct{ key, val string }{
		{"LEANKG_EMBED_DIMS", "x"},
		{"LEANKG_EMBED_DIMS", "-3"},
		{"LEANKG_EMBED_TEXT_CAP", "100"},
		{"LEANKG_EMBED_REQUEST_TIMEOUT_SECS", "0"},
		{"LEANKG_EMBED_SIDECAR_PORT", "70000"},
		{"LEANKG_EMBED_SIDECAR_READY_SECS", "-1"},
	} {
		_, err := Load(lookupFrom(map[string]string{tc.key: tc.val}))
		if err == nil {
			t.Errorf("%s=%q: Load returned no error", tc.key, tc.val)
		}
	}
}

// TestLoadSidecarFromEnvironment reads the sidecar knobs into the config the
// embed constructor spawns from.
func TestLoadSidecarFromEnvironment(t *testing.T) {
	cfg, err := Load(lookupFrom(map[string]string{
		"LEANKG_EMBED_SIDECAR_CMD":        "/opt/llama-server",
		"LEANKG_EMBED_SIDECAR_ARGS":       "-m /models/x.gguf --embeddings",
		"LEANKG_EMBED_SIDECAR_PORT":       "9191",
		"LEANKG_EMBED_SIDECAR_READY_SECS": "30",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := cfg.Sidecar
	if s.Command != "/opt/llama-server" || s.Port != 9191 || s.ReadyTimeout != 30*time.Second {
		t.Fatalf("sidecar = %+v", s)
	}
	if want := []string{"-m", "/models/x.gguf", "--embeddings"}; !reflect.DeepEqual(s.Args, want) {
		t.Fatalf("sidecar args = %q, want %q", s.Args, want)
	}
}
