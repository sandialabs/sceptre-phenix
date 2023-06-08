package middleware

import (
	"net/http"
	"phenix/util/plog"
)

func LogFull(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plog.Info("HTTP request", "request", r)
		h.ServeHTTP(w, r)
		plog.Info("HTTP response", "response", w)
	})
}

func LogRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plog.Info("HTTP request", "method", r.Method, "url", r.RequestURI)
		h.ServeHTTP(w, r)
	})
}
