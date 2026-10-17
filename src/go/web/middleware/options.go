package middleware

import (
	"net/http"
)

// AnswerOptions answers an OPTIONS request with 204 and no body. Every API route
// takes OPTIONS and its handler serves whatever method reaches it, so without
// this an OPTIONS request would run the route's action, such as a delete.
func AnswerOptions(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)

			return
		}

		next.ServeHTTP(w, r)
	})
}
