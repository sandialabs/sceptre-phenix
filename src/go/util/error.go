package util

import (
	"errors"
	"fmt"
	"phenix/util/plog"

	"strings"

	"github.com/gofrs/uuid"
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

func HumanizeError(err error, desc string, a ...interface{}) *HumanizedError {
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

func (this HumanizedError) Error() string {
	return this.cause.Error()
}

func (this HumanizedError) Unwrap() error {
	return this.cause
}

func (this HumanizedError) Humanize() string {
	if this.humanized == "" {
		err := strings.Split(this.cause.Error(), " ")
		err[0] = strings.Title(err[0])

		return strings.Join(err, " ")
	}

	return fmt.Sprintf("%s (search error logs for %s)", this.humanized, this.uuid)
}

func (this HumanizedError) Humanized() error {
	return fmt.Errorf(this.Humanize())
}

func (this HumanizedError) UUID() string {
	return this.uuid
}
