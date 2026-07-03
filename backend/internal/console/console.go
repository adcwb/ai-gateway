// Package console embeds the built web console (frontend/dist) so the gateway
// ships as a single binary (docs/design/08-web-console.md). Run `make web` at
// the repo root to build the frontend and refresh the embedded assets; without
// it a placeholder page is served.
package console

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the SPA with index.html fallback for client-side routes.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" {
			if f, err := sub.Open(path); err == nil {
				f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		// SPA fallback: serve index.html for unknown paths
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
