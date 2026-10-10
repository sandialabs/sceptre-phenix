package weberror

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"phenix/store"
	"phenix/util/plog"
)

type WebError struct {
	Cause          string            `json:"cause"`
	Status         int               `json:"-"`
	Message        string            `json:"message"`
	SystemMetadata map[string]string `json:"sys_metadata,omitempty"` // logged, but not return to user
	UserMetadata   map[string]string `json:"metadata,omitempty"`     // logged and returned to user

	// Code is a stable, machine-readable name of the failure. The Builder
	// routes give every error one. Other routes leave it out.
	Code string `json:"code,omitempty"`
	// Issues lists the problems of the failure, one by one, where a route
	// names them (the Builder routes, as issue objects). Otherwise it is left
	// out.
	Issues any `json:"issues,omitempty"`

	// wrapped is the error the WebError was made from, if any.
	wrapped error
}

func NewWebError(cause error, format string, args ...any) *WebError {
	causeStr := ""

	if cause != nil {
		causeStr = cause.Error()
	}

	err := &WebError{ //nolint:exhaustruct // partial initialization
		Message: fmt.Sprintf(format, args...),
		Cause:   causeStr,
		Status:  http.StatusBadRequest,
		wrapped: cause,
	}

	return err
}

func (err *WebError) WithMetadata(k, v string, user bool) *WebError {
	if err.SystemMetadata == nil {
		err.SystemMetadata = make(map[string]string)
	}

	err.SystemMetadata[k] = v

	if user {
		if err.UserMetadata == nil {
			err.UserMetadata = make(map[string]string)
		}

		err.UserMetadata[k] = v
	}

	return err
}

func (err *WebError) SetStatus(status int) *WebError {
	err.Status = status

	return err
}

// WithCode sets the code of the failure (see [WebError.Code]).
func (err *WebError) WithCode(code string) *WebError {
	err.Code = code

	return err
}

// WithIssues sets the problems the failure is made of (see
// [WebError.Issues]).
func (err *WebError) WithIssues(issues any) *WebError {
	err.Issues = issues

	return err
}

func (err WebError) Error() string {
	if err.Cause == "" {
		return err.Message
	}

	return fmt.Sprintf("%s: %v", err.Message, err.Cause)
}

// Unwrap returns the error the WebError was made from, so [errors.Is] and
// [errors.As] see through it.
func (err WebError) Unwrap() error {
	return err.wrapped
}

type ErrorHandler func(http.ResponseWriter, *http.Request) error

func (err ErrorHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := err(w, r); err != nil {
		web := &WebError{} //nolint:exhaustruct // partial initialization

		ok := errors.As(err, &web)
		logged := web.Error()

		// A write that etcd refused for lack of space gets the same answer on
		// every route: 507 with the message of the store, which says what is
		// wrong and who can fix it. That message replaces one that only names
		// the operation. The replaced error is logged, and it holds the store
		// message once.
		if errors.Is(err, store.ErrNoSpace) {
			web = &WebError{
				Cause:          "",
				Status:         http.StatusInsufficientStorage,
				Message:        store.ErrNoSpace.Error(),
				SystemMetadata: web.SystemMetadata,
				UserMetadata:   web.UserMetadata,
				Code:           web.Code,
				Issues:         nil,
				wrapped:        err,
			}
			logged = err.Error()
			ok = true
		}

		if !ok {
			w.WriteHeader(http.StatusInternalServerError)

			return
		}

		var attrs []any
		for key, value := range web.SystemMetadata {
			attrs = append(attrs, key, value)
		}

		plog.Error(plog.TypeSystem, logged, attrs...)

		web.SystemMetadata = nil
		body, _ := json.Marshal(web)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(web.Status)
		_, _ = w.Write(body)
	}
}
