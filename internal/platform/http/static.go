package httpx

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"
)

// MountSPA serves apps/web's built static assets (index.html, /assets/*)
// from dir, with a client-side-routing fallback: any GET that doesn't
// match a real file (or an /api/* or /health/* path, both already
// registered on r before this is called) gets index.html instead of a
// 404, so a hard refresh on e.g. /sales works the same as a client-side
// navigation there.
//
// A no-op if dir doesn't exist — true for `go run`/`-migrate` and for
// local development, where apps/web runs under its own `vite` dev server
// instead (see apps/web/vite.config.ts's /api proxy). Only the Docker
// image (server.Dockerfile's webbuild stage copies apps/web/dist here)
// actually has this directory.
func MountSPA(r chi.Router, dir string) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return
	}

	// The install manifest is a real file; give it its proper type rather than
	// whatever the host's mime table (often none) says.
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")

	fileServer := http.FileServer(http.Dir(dir))
	indexPath := filepath.Join(dir, "index.html")

	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			http.NotFound(w, req)
			return
		}

		// Serve the file directly if it really exists (JS/CSS/image
		// assets, favicon, etc.) — anything else falls through to
		// index.html so the SPA's own router (TanStack Router) handles
		// the path client-side.
		if requested := filepath.Join(dir, filepath.Clean(req.URL.Path)); requested != indexPath {
			if st, err := os.Stat(requested); err == nil && !st.IsDir() {
				fileServer.ServeHTTP(w, req)
				return
			}
		}

		http.ServeFile(w, req, indexPath)
	})
}
