package auto

import (
	"net/http"

	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/core"
)

// gatedAutoConfig is RegisterAutoConfig under the local-default gate: no token
// is configured, so every caller resolves to Admin. The listener mounts the
// route under the gate in production (serve, main.go and helpers.go).
func gatedAutoConfig(h http.Handler, engine *core.Engine, opts ...Option) http.Handler {
	return auth.MiddlewareWithStore(nil, RegisterAutoConfig(h, engine, opts...))
}
