// Package dashboard embeds the built web dashboard (Vue, see package.json)
// into the core binary and serves it under /admin/: files from dist/app,
// hashed assets cached for a year, and index.html for every other path so
// the app's own routes work on reload.
package dashboard

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var files embed.FS

// Handler serves the dashboard (or a notice when it was not built).
func Handler() http.Handler {
	app, err := fs.Sub(files, "dist/app")
	if err != nil {
		return notBuilt()
	}
	return handler(app)
}

func handler(app fs.FS) http.Handler {
	index, err := fs.ReadFile(app, "index.html")
	if err != nil {
		return notBuilt()
	}
	static := http.FileServerFS(app)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/admin")
		name = strings.TrimPrefix(name, "/")
		if name != "" && name != "index.html" {
			if st, err := fs.Stat(app, name); err == nil && !st.IsDir() {
				if strings.HasPrefix(name, "assets/") { // content-hashed file names
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				r2 := r.Clone(r.Context())
				r2.URL.Path = "/" + name
				static.ServeHTTP(w, r2)
				return
			}
			if strings.HasPrefix(name, "assets/") {
				http.NotFound(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(index)
	})
}

func notBuilt() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("The BOBRES dashboard was not built into this binary (run: make dashboard, then rebuild core).\n"))
	})
}
