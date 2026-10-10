package dashboard

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

// The SPA build is synced into embed/ by `make go-ui-dashboard`; the checked-in
// tree carries a placeholder so the CGO-free build always works.
//
//go:embed all:embed
var assets embed.FS

// spaHandler serves the embedded UI. Paths with a file extension must exist
// (a missing asset is a 404); any other unknown path is a client route and
// gets index.html, so the SPA router can resolve it.
func spaHandler() http.Handler {
	sub, err := fs.Sub(assets, "embed")
	if err != nil {
		panic("dashboard: embed subdir missing: " + err.Error())
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if info, err := fs.Stat(sub, p); err == nil && !info.IsDir() {
			serveEmbedded(w, r, sub, p)
			return
		}
		if path.Ext(p) != "" {
			http.NotFound(w, r)
			return
		}
		serveEmbedded(w, r, sub, "index.html")
	})
}

func serveEmbedded(w http.ResponseWriter, r *http.Request, sub fs.FS, name string) {
	data, err := fs.ReadFile(sub, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ct := mime.TypeByExtension(path.Ext(name))
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-cache")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(data)
}
