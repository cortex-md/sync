package apidocs

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"net/http"

	"github.com/go-chi/chi/v5"
)

//go:embed openapi.yaml
var Spec []byte

//go:embed index.html
var page []byte

//go:embed init.js
var initScript []byte

const contentSecurityPolicy = "default-src 'none'; script-src 'self' https://cdn.jsdelivr.net; style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net; img-src 'self' data: https:; font-src https://cdn.jsdelivr.net; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

func Routes(r chi.Router) {
	r.Get("/docs", serve(page, "text/html; charset=utf-8"))
	r.Get("/docs/", http.RedirectHandler("/docs", http.StatusMovedPermanently).ServeHTTP)
	r.Get("/docs/init.js", serve(initScript, "text/javascript; charset=utf-8"))
	r.Get("/docs/openapi.yaml", serve(Spec, "application/yaml; charset=utf-8"))
}

func serve(body []byte, contentType string) http.HandlerFunc {
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	return func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("Content-Type", contentType)
		header.Set("Content-Security-Policy", contentSecurityPolicy)
		header.Set("Referrer-Policy", "no-referrer")
		header.Set("Cache-Control", "public, max-age=300")
		header.Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write(body)
	}
}
