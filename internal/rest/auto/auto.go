package auto

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/embed"
	"github.com/FreePeak/LeanKG/internal/index"
	"github.com/FreePeak/LeanKG/internal/langs"
	"github.com/FreePeak/LeanKG/internal/refresh"
	"github.com/FreePeak/LeanKG/internal/store"
)

// AutoConfigRequest controls the MCP auto-init behaviors served over REST.
// POST /api/v1/mcp/auto-config — no project parameter; always operates on
// the served project's own engine (the default project when no router is set).
type AutoConfigRequest struct {
	IndexEnabled      bool `json:"index_enabled"`
	EmbedEnabled      bool `json:"embed_enabled"`
	EmbedDebounceSecs int  `json:"embed_debounce_seconds,omitempty"`
}

var (
	autoIndexMu      sync.Mutex
	autoIndexRunning bool
	autoEmbedMu      sync.Mutex
	autoEmbedRunning bool
)

func decodeJSON[T any](w http.ResponseWriter, r *http.Request, into *T) bool {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewDecoder(r.Body).Decode(into); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func writeAutoJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// RegisterAutoConfig mounts POST /api/v1/mcp/auto-config on the mux.
// The endpoint triggers background auto-index and/or auto-embed for the
// served project; both default to off and must be explicitly enabled.
func RegisterAutoConfig(h http.Handler, engine *core.Engine) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", h)
	mux.HandleFunc("POST /api/v1/mcp/auto-config", func(w http.ResponseWriter, r *http.Request) {
		handleAutoConfig(w, r, engine)
	})
	return mux
}

func handleAutoConfig(w http.ResponseWriter, r *http.Request, engine *core.Engine) {
	var req AutoConfigRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.EmbedDebounceSecs == 0 {
		req.EmbedDebounceSecs = 120
	}

	ctx := r.Context()
	projectDir := engine.ProjectDir()
	st := engine.Store()

	if req.IndexEnabled {
		go startAutoIndex(ctx, st, projectDir)
	}
	if req.EmbedEnabled {
		go startAutoEmbed(ctx, st, projectDir, req.EmbedDebounceSecs)
	}

	writeAutoJSON(w, http.StatusOK, map[string]any{
		"index_enabled":          req.IndexEnabled,
		"embed_enabled":          req.EmbedEnabled,
		"embed_debounce_seconds": req.EmbedDebounceSecs,
	})
}

// startAutoIndex runs the index in a background goroutine (single-flight per
// process: a second call while one is in flight is a no-op with a logged note).
func startAutoIndex(ctx context.Context, st store.Backend, dir string) {
	autoIndexMu.Lock()
	if autoIndexRunning {
		autoIndexMu.Unlock()
		log.Printf("auto-config: index already running, skipping")
		return
	}
	autoIndexRunning = true
	autoIndexMu.Unlock()
	defer func() {
		autoIndexMu.Lock()
		autoIndexRunning = false
		autoIndexMu.Unlock()
	}()

	if _, err := os.Stat(filepath.Join(dir, ".leankg")); err != nil {
		log.Printf("auto-config: index skipped: no .leankg at %s", dir)
		return
	}
	langReg := langs.DefaultRegistry()
	if _, aerr := langReg.Activate(dir); aerr != nil {
		log.Printf("auto-config: index language detection: %v", aerr)
		return
	}
	res, err := index.IndexDirWith(ctx, st, dir, langReg)
	if err != nil {
		log.Printf("auto-config: index failed: %v", err)
		return
	}
	if _, ierr := store.RefreshInventory(st); ierr != nil {
		log.Printf("auto-config: index inventory refresh: %v", ierr)
	}
	log.Printf("auto-config: index complete: files=%d elements=%d relationships=%d", res.Files, res.Elements, res.Relationships)
}

// startAutoEmbed waits the debounce window for index to settle, then runs
// the embedding pipeline in a background goroutine (single-flight per process).
func startAutoEmbed(ctx context.Context, st store.Backend, dir string, debounceSecs int) {
	autoEmbedMu.Lock()
	if autoEmbedRunning {
		autoEmbedMu.Unlock()
		log.Printf("auto-config: embed already running, skipping")
		return
	}
	autoEmbedRunning = true
	autoEmbedMu.Unlock()
	defer func() {
		autoEmbedMu.Lock()
		autoEmbedRunning = false
		autoEmbedMu.Unlock()
	}()

	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Duration(debounceSecs) * time.Second):
	}

	provider, release, err := embed.StartProvider(ctx)
	if err != nil {
		log.Printf("auto-config: embed skipped: %v", err)
		return
	}
	defer release()
	_ = provider

	res, err := refresh.Run(ctx, refresh.Options{Project: dir, Path: dir})
	if err != nil {
		log.Printf("auto-config: embed failed: %v", err)
		return
	}
	if res.EmbedSkipped != "" {
		log.Printf("auto-config: embed skipped: %s", res.EmbedSkipped)
	} else {
		log.Printf("auto-config: embed complete: embedded=%d skipped=%d failed=%d", res.Embed.Embedded, res.Embed.Skipped, res.Embed.Failed)
	}
}
