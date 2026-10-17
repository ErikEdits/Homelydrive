package api

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// serveStatic serves the built web UI; unknown paths fall back to index.html
// so the single-page app can handle its own routes.
func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSON(w, http.StatusNotFound, apiError{Error: "Unbekannter API-Pfad.", Code: "not_found"})
		return
	}
	if s.Web == nil {
		http.Error(w, "Web UI not built (run `npm run build` in web/)", http.StatusNotFound)
		return
	}
	p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if p != "" && p != "index.html" && p != "admin.html" {
		if fi, err := fs.Stat(s.Web, p); err == nil && !fi.IsDir() {
			switch {
			case strings.HasPrefix(p, "assets/"):
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			case p == "sw.js":
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Service-Worker-Allowed", "/")
			default:
				w.Header().Set("Cache-Control", "public, max-age=3600")
			}
			http.ServeFileFS(w, r, s.Web, p)
			return
		}
	}
	s.servePage("index.html")(w, r)
}

func (s *Server) servePage(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Web == nil {
			http.Error(w, "Web UI not built (run `npm run build` in web/)", http.StatusNotFound)
			return
		}
		b, err := fs.ReadFile(s.Web, name)
		if err != nil {
			http.Error(w, "Web UI not built (run `npm run build` in web/)", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; style-src 'self' 'unsafe-inline'; script-src 'self'; frame-src 'self'; object-src 'self'; connect-src 'self'; worker-src 'self'; manifest-src 'self'")
		w.Write(b)
	}
}
