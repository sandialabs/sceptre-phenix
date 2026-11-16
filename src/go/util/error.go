package util

import (
	"errors"
	"fmt"
	"unicode"
	"unicode/utf8"

	"github.com/gofrs/uuid/v5"

	"phenix/util/plog"
)

func LogErrorGetID(err error) string {
	uuid := uuid.Must(uuid.NewV4()).String()
	plog.Error(plog.TypeSystem, err.Error(), "uuid", uuid)

	return uuid
}

type HumanizedError struct {
	cause     error
	humanized string
	uuid      string
}

func HumanizeError(err error, desc string, a ...any) *HumanizedError {
	var h *HumanizedError

	if errors.As(err, &h) {
		return h
	}

	h = &HumanizedError{cause: err, humanized: fmt.Sprintf(desc, a...), uuid: ""}
	h.uuid = LogErrorGetID(h)

	return h
}

// Error returns the message of the cause. With no cause, it returns the description, or "unknown error".
func (e HumanizedError) Error() string {
	switch {
	case e.cause != nil:
		return e.cause.Error()
	case e.humanized != "":
		return e.humanized
	default:
		return "unknown error"
	}
}

func (e HumanizedError) Unwrap() error {
	return e.cause
}

func (e HumanizedError) Humanize() string {
	if e.humanized == "" {
		msg := e.Error()

		first, size := utf8.DecodeRuneInString(msg)
		if first == utf8.RuneError {
			return msg
		}

		return string(unicode.ToUpper(first)) + msg[size:]
	}

	return fmt.Sprintf("%s (search error logs for %s)", e.humanized, e.uuid)
}

func (e HumanizedError) Humanized() error {
	return errors.New(e.Humanize())
}

func (e HumanizedError) UUID() string {
	return e.uuid
}
