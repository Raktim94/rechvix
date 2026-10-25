package httpx

import "net/http"

// contentSecurityPolicy stays same-origin. The extra grants exist only for
// in-browser bill scanning (tesseract.js): a WASM core compiled from our own
// files, a worker, and image previews from blob:/data: URLs.
const contentSecurityPolicy = "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; " +
	"worker-src 'self' blob:; img-src 'self' data: blob:; frame-ancestors 'none'"

// SecurityHeaders sets the response headers required by brief §59. CSP is
// deliberately restrictive by default (same-origin only); a deployment
// serving the SPA from a different origin than the API, or embedding
// third-party widgets, adjusts this via an explicit, reviewed change —
// not by loosening it here as a default.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		// HSTS only makes sense once TLS terminates somewhere in front of
		// this request — set unconditionally here because
		// docs/architecture.md §12 makes TLS termination the deployment
		// operator's responsibility; a plain-HTTP browser simply ignores
		// this header, so it's harmless to send from a backend that itself
		// only ever speaks HTTP inside the private network.
		h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		// Clickjacking protection: CSP frame-ancestors above is the modern
		// mechanism; X-Frame-Options is kept for older browsers.
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// CORS returns an explicit, allow-listed CORS middleware. There is no
// wildcard-origin mode here at all (brief §59: "Never use wildcard CORS
// with credentials") — allowedOrigins must be a concrete, non-empty list
// for cross-origin requests to be permitted; an empty list means same-
// origin only (the SPA served by this same server, per
// docs/architecture.md §12's "embed the built frontend" option).
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				if _, ok := allowed[origin]; ok {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Vary", "Origin")
					w.Header().Set("Access-Control-Allow-Credentials", "true")
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Idempotency-Key")
				}
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
