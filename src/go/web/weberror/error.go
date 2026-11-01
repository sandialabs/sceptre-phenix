package weberror

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"phenix/util/plog"
)

type WebError struct {
	Cause          string            `json:"cause"`
	Status         int               `json:"-"`
	Message        string            `json:"message"`
	Code           Code              `json:"code,omitempty"`
	SystemMetadata map[string]string `json:"sys_metadata,omitempty"` // logged, but not return to user
	UserMetadata   map[string]string `json:"metadata,omitempty"`     // logged and returned to user
	Details        map[string]any    `json:"details,omitempty"`      // structured details some codes carry
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

// WithDetail sets the structured detail k, returned to the user and not logged, such as the list of
// broken disks a CodeBrokenDisks error carries.
func (err *WebError) WithDetail(k string, v any) *WebError {
	if err.Details == nil {
		err.Details = make(map[string]any)
	}

	err.Details[k] = v

	return err
}

func (err *WebError) SetStatus(status int) *WebError {
	err.Status = status

	return err
}

// WithCode sets the error's code, for an error whose status's default code
// (see CodeFor) does not fit.
func (err *WebError) WithCode(code Code) *WebError {
	err.Code = code

	return err
}

func (err WebError) Error() string {
	if err.Cause == "" {
		return err.Message
	}

	return fmt.Sprintf("%s: %v", err.Message, err.Cause)
}

// internalMessage is the message of a 500 for an error that is not a
// WebError, whose text is logged rather than sent.
const internalMessage = "internal server error"

// New returns the error a handler answers with status and message, as
// [http.Error] would send them, but as the JSON Error body.
func New(status int, message string) *WebError {
	return &WebError{ //nolint:exhaustruct // partial initialization
		Message: message,
		Status:  status,
	}
}

// Error answers the request with status and message as the JSON Error body. It
// takes the place of [http.Error] where the code cannot return the error.
func Error(w http.ResponseWriter, message string, status int) {
	Write(w, New(status, message))
}

// UpgradeError answers a failed websocket handshake as the JSON Error body; it is a gorilla Upgrader's Error.
func UpgradeError(w http.ResponseWriter, _ *http.Request, status int, reason error) {
	w.Header().Set("Sec-Websocket-Version", "13")
	Error(w, reason.Error(), status)
}

type ErrorHandler func(http.ResponseWriter, *http.Request) error

func (err ErrorHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := err(w, r); err != nil {
		web := &WebError{} //nolint:exhaustruct // partial initialization

		if !errors.As(err, &web) {
			plog.Error(plog.TypeSystem, "handling request", "method", r.Method, "path", r.URL.Path, "err", err)

			web = New(http.StatusInternalServerError, internalMessage)
		}

		Write(w, web)
	}
}

// Write logs err, at error level for a server error and debug level otherwise, and answers it as the JSON
// Error body, defaulting its code and message from its status and leaving out its system metadata.
func Write(w http.ResponseWriter, err *WebError) {
	if err.Message == "" {
		err.Message = http.StatusText(err.Status)
	}

	attrs := make([]any, 0, 2*len(err.SystemMetadata)) //nolint:mnd // key and value
	for key, value := range err.SystemMetadata {
		attrs = append(attrs, key, value)
	}

	if err.Status >= http.StatusInternalServerError {
		plog.Error(plog.TypeSystem, err.Error(), attrs...)
	} else {
		plog.Debug(plog.TypeSystem, err.Error(), attrs...)
	}

	err.SystemMetadata = nil

	if err.Code == "" {
		err.Code = CodeFor(err.Status)
	}

	body, _ := json.Marshal(err)

	header := w.Header()
	header.Del("Content-Length")
	header.Set("Content-Type", "application/json")
	header.Set("X-Content-Type-Options", "nosniff")

	w.WriteHeader(err.Status)
	_, _ = w.Write(body)
}
