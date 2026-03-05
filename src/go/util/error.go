package util

import (
	"errors"
	"fmt"
	"strings"

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

	return &HumanizedError{
		cause:     err,
		humanized: fmt.Sprintf(desc, a...),
		uuid:      LogErrorGetID(err),
	}
}

func (e HumanizedError) Error() string {
	return e.cause.Error()
}

func (e HumanizedError) Unwrap() error {
	return e.cause
}

func (e HumanizedError) Humanize() string {
	if e.humanized == "" {
		err := strings.Split(e.cause.Error(), " ")
		err[0] = strings.ToUpper(err[0][:1]) + err[0][1:]

		return strings.Join(err, " ")
	}

	return fmt.Sprintf("%s (search error logs for %s)", e.humanized, e.uuid)
}

func (e HumanizedError) Humanized() error {
	return errors.New(e.Humanize())
}

func (e HumanizedError) UUID() string {
	return e.uuid
}
