package embed

// Pinned model catalog — issue #279 / FR-ZCP-11 part 1, ported from the Rust
// engine's model registry (git show 'f7624143^:src/embeddings/registry.rs',
// builtin_registry) and extended with the two things that reference tree never
// had, per zvec-grep's catalog design: a commit-level source pin and a paired
// query/document text prefix.
//
// Where every value here came from (nothing invented):
//
//   - model ids, provider kinds, model names, dimensions: copied from the
//     reference registry.rs builtin_registry rows.
//   - the local models' ONNX artifact repos: reference src/embeddings/models.rs
//     (Xenova/bge-small-en-v1.5, Xenova/all-MiniLM-L6-v2 — the repos the ONNX
//     cache directory names).
//   - 40-hex commit revisions: each repo's head commit, resolved live against
//     https://huggingface.co/api/models/<repo> on 2026-09-13.
//   - query/document prefixes: each model's own
//     config_sentence_transformers.json "prompts" block, read live from the
//     repo. Empty where the repo publishes no prompts — that is a sourced
//     value, not a gap (BGE-small-en-v1.5 and MiniLM embed raw text, which is
//     also what the reference's DirectEmbedder does).
//
// Revision precedence (Pin):
//
//  1. LEANKG_EMBED_REVISION when set — an explicit operator pin always wins
//     (mirrored artifact, internal fork, deliberately older snapshot).
//  2. the catalog pin for the selected model.
//  3. for a model with no catalog row: "<provider>:<model>", so a model switch
//     still invalidates the stamp. This is also what a llama.cpp sidecar
//     serving an arbitrary GGUF gets.

import (
	"fmt"
	"os"
	"strings"
)

// Distance is the metric every catalog row advertises; the reference registry
// uses cosine for all six models.
const Distance = "cosine"

// DefaultModelID is the default local collection (reference registry.rs
// DEFAULT_BGE_MODEL_ID): BGE-small-en-v1.5 at 384 dimensions.
const DefaultModelID = "bge-small-en-v1.5-384"

// Model is one pinned catalog row.
type Model struct {
	// ID is the collection key the stamp records (reference model_id).
	ID string
	// Provider is "local" or "openai" — the stamp's provider component.
	Provider string
	// Name is the provider-facing model name: the ONNX variant for local
	// rows, the API model id sent in the request body for remote rows.
	Name string
	// Repo is the HuggingFace repo the artifact came from; empty for
	// provider-hosted models with no public artifact repo (Gemini).
	Repo string
	// Revision is the pinned source identity: a 40-hex HF commit for
	// artifact-backed rows, else the provider's version pin.
	Revision string
	// Endpoint is the OpenAI-compatible API root that serves Name ("" for
	// local rows, whose URL is environment-owned). Reference
	// src/embeddings/provider.rs: the client appends only "/embeddings", so
	// the versioned root must carry its own prefix — Google's compat layer has
	// no "/v1" segment.
	Endpoint string
	// Dims is the vector width the model produces.
	Dims int
	// QueryPrefix / DocumentPrefix are the asymmetric text prefixes the model
	// was trained with. The provider applies them at BOTH document and query
	// time, and they are recorded on the stamp so a prefix change can never
	// mix one collection.
	QueryPrefix    string
	DocumentPrefix string
}

// catalogRows is the pinned registry; lookup goes through the index maps.
var catalogRows = []Model{
	{
		// Reference registry.rs default local entry (384-d ONNX BGE-small).
		// Prompts: none. Xenova/bge-small-en-v1.5 publishes no
		// config_sentence_transformers.json prompts block, and the BGE v1.5
		// English card needs no query instruction — issue #279 ground truth:
		// "Represent this sentence for searching relevant passages: " is the
		// v1/zh family convention, and whether en-v1.5 benefits from it is an
		// open A/B on the ANN probe, so the pin stays empty until that probe
		// says otherwise.
		ID:       DefaultModelID,
		Provider: "local",
		Name:     "bge-small-en-v1.5",
		Repo:     "Xenova/bge-small-en-v1.5",
		Revision: "ea104dacec62c0de699686887e3f920caeb4f3e3",
		Dims:     384,
	},
	{
		// Reference models.rs EmbedModelKind::MiniLm ("faster, still 384-d").
		// Prompts: none — all-MiniLM-L6-v2 publishes no prompts block and the
		// reference embeds raw text for it too.
		ID:       "all-minilm-l6-v2-384",
		Provider: "local",
		Name:     "all-MiniLM-L6-v2",
		Repo:     "Xenova/all-MiniLM-L6-v2",
		Revision: "751bff37182d3f1213fa05d7196b954e230abad9",
		Dims:     384,
	},
	{
		// Reference registry.rs "qwen3-emb-4b-2560" (which pinned the version
		// string "api:2026-01"); the repo is HF-hosted, so the artifact commit
		// is the stronger pin and the API model id stays visible in Name.
		ID:       "qwen3-emb-4b-2560",
		Provider: "openai",
		Name:     "Qwen/Qwen3-Embedding-4B",
		Repo:     "Qwen/Qwen3-Embedding-4B",
		Revision: "5cf2132abc99cad020ac570b19d031efec650f2b",
		Dims:     2560,
		// config_sentence_transformers.json prompts.query; prompts.document
		// is "", i.e. documents stay raw.
		QueryPrefix: "Instruct: Given a web search query, retrieve relevant passages that answer the query\nQuery:",
	},
	{
		// Reference registry.rs "jina-embeddings-v3-1024".
		ID:       "jina-embeddings-v3-1024",
		Provider: "openai",
		Name:     "jina-embeddings-v3",
		Repo:     "jinaai/jina-embeddings-v3",
		Endpoint: "https://api.jina.ai/v1",
		Revision: "ab036b023d30b4d1138c4c3bfa9f0c445ab455d6",
		Dims:     1024,
		// config_sentence_transformers.json prompts: retrieval.query /
		// retrieval.passage.
		QueryPrefix:    "Represent the query for retrieving evidence documents: ",
		DocumentPrefix: "Represent the document for retrieval: ",
	},
	{
		// Reference registry.rs "gemini-embedding-2-3072". Google serves this
		// model from its own API: there is no artifact repo to pin, so the
		// identity stays a version string. Its retrieval asymmetry rides a
		// `task_type` REQUEST field, not a text prefix, and this client sends
		// the plain OpenAI /embeddings body — so no prefixes (named in the
		// completion report as a not-carried-parameter gap, not a guess).
		ID:       "gemini-embedding-2-3072",
		Provider: "openai",
		Name:     "gemini-embedding-2",
		Endpoint: "https://generativelanguage.googleapis.com/v1beta/openai",
		Revision: "api:gemini-embedding-2",
		Dims:     3072,
	},
	{
		// Reference registry.rs "gemini-embedding-001-3072". Same rationale.
		ID:       "gemini-embedding-001-3072",
		Provider: "openai",
		Name:     "gemini-embedding-001",
		Endpoint: "https://generativelanguage.googleapis.com/v1beta/openai",
		Revision: "api:gemini-embedding-001",
		Dims:     3072,
	},
}

// byID and byName index catalogRows; both are built once at init and the
// catalog is immutable afterwards.
var (
	byID   = map[string]Model{}
	byName = map[string]Model{}
)

func init() {
	for _, m := range catalogRows {
		byID[m.ID] = m
		// Names are matched case-insensitively and trimmed: what an operator
		// puts in LEANKG_EMBED_MODEL is whatever the serving layer advertises.
		for _, key := range []string{m.Name, m.ID, m.Repo} {
			if key != "" {
				byName[normalizeModel(key)] = m
			}
		}
	}
}

func normalizeModel(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// Models returns the pinned catalog rows (status/doctor reporting).
func Models() []Model { return append([]Model(nil), catalogRows...) }

// Lookup resolves a catalog row by collection id, provider-facing model name
// or HF repo. ok=false for an uncatalogued model.
func Lookup(model string) (Model, bool) {
	m, ok := byName[normalizeModel(model)]
	return m, ok
}

// Pin returns the revision to stamp for a model, following the precedence
// documented at the top of this file.
func Pin(model, provider string) string {
	if r := strings.TrimSpace(os.Getenv("LEANKG_EMBED_REVISION")); r != "" {
		return r
	}
	if m, ok := Lookup(model); ok {
		return m.Revision
	}
	return provider + ":" + model
}

// Prefixes returns the pinned query/document text prefixes for a model, or
// two empty strings for an uncatalogued model: an unknown model gets no
// instruction rather than a guessed one.
func Prefixes(model string) (query, document string) {
	if m, ok := Lookup(model); ok {
		return m.QueryPrefix, m.DocumentPrefix
	}
	return "", ""
}

// EndpointFor returns the catalog's API root for a model ("" when the model
// is unknown or local, in which case the environment owns the URL).
func EndpointFor(model string) string {
	if m, ok := Lookup(model); ok {
		return m.Endpoint
	}
	return ""
}

// ResolveDims picks the vector width for a model: an explicit non-zero
// LEANKG_EMBED_DIMS wins, then the catalog pin, then fallback.
func ResolveDims(model string, explicit, fallback int) (int, error) {
	if explicit > 0 {
		return explicit, nil
	}
	if m, ok := Lookup(model); ok {
		return m.Dims, nil
	}
	if fallback <= 0 {
		return 0, fmt.Errorf("embed: dimensions required for uncatalogued model %q (set LEANKG_EMBED_DIMS)", model)
	}
	return fallback, nil
}
