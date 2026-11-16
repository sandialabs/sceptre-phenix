package web

import (
	"context"
	"errors"
	"net/http"

	"phenix/util/plog"
	"phenix/web/middleware"
)

// requestGone reports whether the request has ended, its client having left
// or the server shutting down. The minimega commands its handler had not yet
// sent then give up (see mm.Context), and the handler writes nothing more: no
// one is left to read it.
func requestGone(r *http.Request) bool {
	return r.Context().Err() != nil
}

// requestCanceled reports whether err is the request's minimega commands
// giving up because the request ended (see requestGone), and if so logs it at
// debug level. A client leaving is no error: the handler then logs nothing
// else at error level and writes nothing.
func requestCanceled(r *http.Request, err error) bool {
	if !endedWith(r.Context(), err) {
		return false
	}

	plog.Debug(
		plog.TypeHTTP, "request canceled",
		"method", r.Method, "url", middleware.RedactURI(r.RequestURI), "err", err,
	)

	return true
}

// endedWith reports whether err is commands run for ctx giving up because ctx
// is done (see mm.Context).
func endedWith(ctx context.Context, err error) bool {
	ctxErr := ctx.Err()

	return err != nil && ctxErr != nil && errors.Is(err, ctxErr)
}
