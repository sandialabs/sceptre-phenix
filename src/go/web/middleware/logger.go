package middleware

import (
	"bufio"
	"errors"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"regexp"

	"phenix/util/plog"
	"phenix/web/rbac"
)

var tokenRegex = regexp.MustCompile(`(token=)(.+)(\?|$)`)

// LogRequests logs requests along with ip/user/role that made the request
// this method redacts tokens in GET parameters.
func LogRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := r.Context().Value(ContextKeyUser)

		role := r.Context().Value(ContextKeyRole)
		if role != nil {
			roleObj, _ := role.(rbac.Role)
			role = roleObj.Spec.Name
		}

		plog.Info(plog.TypeHTTP, "HTTP request",
			"method", r.Method,
			"url", tokenRegex.ReplaceAllString(r.RequestURI, "${1}REDACTED"),
			"address", r.RemoteAddr,
			"user", user,
			"role", role)
		h.ServeHTTP(w, r)
	})
}

// LogFull logs all http requests and responses completely, including sensitive data (e.g., passwords)
// Intended for development only. Does not log websocket messages.
func LogFull(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plog.Warn(
			plog.TypeSystem,
			"FULL HTTP LOGGING ENABLED. OUTPUT WILL CONTAIN SENSITIVE DATA. DEVELOPMENT ONLY",
		)

		req, err := httputil.DumpRequest(r, true)
		if err != nil {
			plog.Error(plog.TypeSystem, "error parsing http req", "err", err)
		} else {
			plog.Info(plog.TypeHTTP, "HTTP request", "request", string(req))
		}

		rec := &HijackableResponseRecorder{writer: w, ResponseRecorder: httptest.NewRecorder(), hijacked: false}
		h.ServeHTTP(rec, r)
		// if hijacked, can't touch anything. This applies for the websocket only
		if rec.hijacked {
			return
		}

		res, err := httputil.DumpResponse(rec.Result(), true)
		if err != nil {
			plog.Error(plog.TypeSystem, "error parsing http res", "err", err)
		} else {
			plog.Info(plog.TypeHTTP, "HTTP response", "response", string(res))
		}

		// this copies the recorded response to the response writer
		maps.Copy(w.Header(), rec.Header())

		w.WriteHeader(rec.Code)
		_, _ = rec.Body.WriteTo(w)
	})
}

type HijackableResponseRecorder struct {
	*httptest.ResponseRecorder

	writer   http.ResponseWriter
	hijacked bool
}

func (r *HijackableResponseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := r.writer.(http.Hijacker); ok {
		r.hijacked = true

		return hj.Hijack()
	}

	return nil, nil, errors.New("not a hijacker")
}
