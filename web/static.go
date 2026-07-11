package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var staticFS embed.FS

// StaticHandler serves the embedded self-hosted assets (fonts, CSS, JS) under
// /static/. Assets are content-stable and safe to cache aggressively. Serving
// is intentionally auth-free (see server.withAuth) so unauthenticated pages can
// still style themselves.
func StaticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err) // embed guarantees web/static exists at build time
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.StripPrefix("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		fileServer.ServeHTTP(w, r)
	}))
}
