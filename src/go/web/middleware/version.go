package middleware

import (
	"net/http"

	"phenix/version"
)

// HeaderVersion names the server's build, as openapi.yml's info.version does
// when the server serves it, so a client can tell which spec it is talking to.
const HeaderVersion = "X-Phenix-Version"

// Version stamps every response with HeaderVersion.
func Version(next http.Handler) http.Handler {
	v := version.API()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(HeaderVersion, v)

		next.ServeHTTP(w, r)
	})
}
