// Package web serves the ui-v2 dashboard (the same checked-in build the
// Rust engine embeds under src/embed/) with go:embed — CGO-free, no build
// step needed. Re-sync with `make go-ui-assets` after a ui-v2 rebuild.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:embed
var assets embed.FS

// Handler serves the UI at / with SPA fallback (unknown paths → index.html)
// and correct content types for the bundled assets.
func Handler() http.Handler {
	sub, err := fs.Sub(assets, "embed")
	if err != nil {
		panic("web: embed subdir missing: " + err.Error())
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(sub, p); err != nil {
			// SPA fallback: client-side routes resolve to index.html.
			if !strings.HasPrefix(p, "assets/") {
				serveFile(w, sub, "index.html")
				return
			}
			http.NotFound(w, r)
			return
		}
		serveFile(w, sub, p)
	})
}

func serveFile(w http.ResponseWriter, sub fs.FS, name string) {
	data, err := fs.ReadFile(sub, name)
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	switch {
	case strings.HasSuffix(name, ".html"):
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case strings.HasSuffix(name, ".js"):
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	case strings.HasSuffix(name, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(name, ".svg"):
		w.Header().Set("Content-Type", "image/svg+xml")
	case strings.HasSuffix(name, ".json"):
		w.Header().Set("Content-Type", "application/json")
	}
	_, _ = w.Write(data)
}
