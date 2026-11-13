package middleware

import (
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

// AnswerOptions answers an OPTIONS request with 204, no body, and an Allow
// header that names the methods allowed returns for the request's path. Every
// API route takes OPTIONS and its handler serves whatever method reaches it, so
// without this an OPTIONS request would run the route's action, such as a delete.
func AnswerOptions(allowed func(*http.Request) []string) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodOptions {
				if methods := allowed(r); len(methods) > 0 {
					w.Header().Set("Allow", strings.Join(methods, ", "))
				}

				w.WriteHeader(http.StatusNoContent)

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
