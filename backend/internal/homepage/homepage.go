// Package homepage embeds the static public homepage (repo-root homepage/)
// so it ships in the single binary alongside the console (docs/superpowers/
// specs/2026-07-10-homepage-and-brand-mark-design.md). `make embed` copies
// homepage/ into dist/; without it a placeholder page is served.
package homepage

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the static homepage. One page, no client-side routing, so
// unlike console.Handler() this needs no SPA fallback.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	return http.FileServer(http.FS(sub))
}
