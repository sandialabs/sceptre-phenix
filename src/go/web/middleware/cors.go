package middleware

import (
	"net/http"
)

const (
	origins = "*"
	methods = "GET, POST, PUT, PATCH, DELETE, OPTIONS"
	headers = "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, " +
		"X-Phenix-Auth-Token, If-Match, If-None-Match"
	// the response headers a cross-origin script may read
	// (Date, to measure X-Phenix-Observed-At against the server's clock).
	exposed = "Date, ETag, Location, X-Phenix-Seq, X-Phenix-Revision, X-Phenix-Observed-At, X-Phenix-Stale"
)

func AllowCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", origins)
		w.Header().Set("Access-Control-Allow-Methods", methods)
		w.Header().Set("Access-Control-Allow-Headers", headers)
		w.Header().Set("Access-Control-Expose-Headers", exposed)

		if r.Method == http.MethodOptions {
			return
		}

		next.ServeHTTP(w, r)
	})
}

// AnswerOptions answers an OPTIONS request with 204 and no body, for a server
// that does not allow CORS. Every API route takes OPTIONS, for preflights, and
// its handler serves whatever method reaches it, so without this an OPTIONS
// request would run the route's action, such as a delete.
func AnswerOptions(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)

			return
		}

		next.ServeHTTP(w, r)
	})
}
