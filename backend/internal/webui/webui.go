// Package webui serves the editor built into the binary. `make build` copies
// frontend/dist into dist/ before compiling; a plain `go build` embeds only
// dist/.gitkeep, and the handler then serves a page saying the editor was
// not built in. See docs/server-v1.md §8.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

// Handler serves the embedded editor.
func Handler() http.Handler {
	dist, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err) // the embed pattern guarantees dist/ exists
	}
	return newHandler(dist)
}

// Built reports whether an editor build is embedded.
func Built() bool {
	_, err := fs.Stat(embedded, "dist/index.html")
	return err == nil
}

// newHandler serves the editor files in dist, or the not-built page when
// dist has no index.html.
func newHandler(dist fs.FS) http.Handler {
	if _, err := fs.Stat(dist, "index.html"); err != nil {
		return http.HandlerFunc(notBuilt)
	}
	files := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Vite names assets by content hash, so they never change; the page
		// itself must be revalidated to pick up a new build.
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

const notBuiltPage = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>FlowForge</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 40rem; margin: 3rem auto; line-height: 1.5">
<h1>FlowForge</h1>
<p>This binary was built without the editor, so only the API is served here
(<a href="/api/health">/api/health</a>).</p>
<p>To build the editor in, run <code>make build</code> at the repository root.
During development, run <code>npm run dev</code> in <code>frontend/</code> instead.</p>
</body>
</html>
`

func notBuilt(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(notBuiltPage))
}
