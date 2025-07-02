package weberror

import (
	"encoding/json"
	"fmt"
	"net/http"

	"phenix/util/plog"
)

type WebError struct {
	Cause          string            `json:"cause"`
	Status         int               `json:"-"`
	Message        string            `json:"message"`
	SystemMetadata map[string]string `json:"sys_metadata,omitempty"` // logged, but not return to user
	UserMetadata   map[string]string `json:"metadata,omitempty"`     // logged and returned to user
}

func NewWebError(cause error, format string, args ...interface{}) *WebError {
	causeStr := ""

	if cause != nil {
		causeStr = cause.Error()
	}

	err := &WebError{
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

func (err *WebError) SetStatus(status int) *WebError {
	err.Status = status
	return err
}

func (err WebError) Error() string {
	if err.Cause == "" {
		return err.Message
	}

	return fmt.Sprintf("%s: %v", err.Message, err.Cause)
}

type ErrorHandler func(http.ResponseWriter, *http.Request) error

func (err ErrorHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := err(w, r); err != nil {
		web, ok := err.(*WebError)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		var attrs []any
		for key, value := range web.SystemMetadata {
			attrs = append(attrs, key, value)
		}
		plog.Error(plog.TypeSystem, web.Error(), attrs...)

		web.SystemMetadata = nil
		body, _ := json.Marshal(web)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(web.Status)
		w.Write(body)
	}
}
