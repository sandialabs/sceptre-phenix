package middleware

import (
	"net/http"
)

// AnswerOptions answers an OPTIONS request with 204 and no body, before the
// middleware after it, such as Auth, runs. The router has set the Allow header
// of the request's path.
func AnswerOptions(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)

			return
		}

		next.ServeHTTP(w, r)
	})
}
