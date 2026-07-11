package ui

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Handler serves the embedded UI. It resolves a request path against the
// Next.js static export, trying the export's per-route HTML files
// (`/catalog` -> `catalog.html`, or `catalog/index.html` when trailing slashes
// are on) before falling back to `index.html` for client-only routes. Without
// the per-route resolution every non-asset path served index.html, so every
// page looked identical.
//
// If the embedded export cannot be resolved (a build without the UI embedded),
// Handler degrades gracefully: rather than panic at startup it returns a handler
// that reports the failure with 500 on every request, so the API keeps serving.
func Handler() http.Handler {
	sub, err := fs.Sub(Files, "dist")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "ui: embedded assets unavailable", http.StatusInternalServerError)
		})
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target, ok := resolve(sub, r.URL.Path)
		if !ok {
			// Not a known asset or exported route — serve the SPA entrypoint.
			target = "/"
		}
		rr := r.Clone(r.Context())
		rr.URL.Path = target
		fileServer.ServeHTTP(w, rr)
	})
}

// resolve maps a request path to a real file in the export. It returns the
// path to serve (rooted with "/") and whether a match was found. It tries, in
// order: the exact file (assets like `_next/...`), `<route>.html`, and
// `<route>/index.html`.
func resolve(sub fs.FS, urlPath string) (string, bool) {
	clean := strings.Trim(path.Clean("/"+urlPath), "/")
	if clean == "" {
		return "/", true // root -> index.html via the file server
	}
	for _, candidate := range []string{clean, clean + ".html", clean + "/index.html"} {
		if isFile(sub, candidate) {
			return "/" + candidate, true
		}
	}
	return "", false
}

func isFile(sub fs.FS, name string) bool {
	info, err := fs.Stat(sub, name)
	return err == nil && !info.IsDir()
}
