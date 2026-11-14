package middleware

import (
	"net/http"
	"strconv"

	"phenix/util/freshness"
)

// Freshness stamps every API response with X-Phenix-Seq, taken before the
// handler runs so it precedes every read the handler makes, and marks it
// "Cache-Control: no-cache, private" unless the handler sets its own. A client
// orders a response against websocket messages by the seq: the response
// reflects every change whose message carries a lower seq.
func Freshness(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(freshness.HeaderSeq, strconv.FormatUint(freshness.Seq(), 10))
		w.Header().Set("Cache-Control", "no-cache, private")

		next.ServeHTTP(w, r)
	})
}
