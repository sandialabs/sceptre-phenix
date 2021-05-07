package util

import (
	"context"

	"github.com/gofrs/uuid"
)

type warningsUUID struct{}

var warnings = make(map[string][]error)

func WarningContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}

	uuid := uuid.Must(uuid.NewV4()).String()
	return context.WithValue(ctx, warningsUUID{}, uuid)
}

func AddWarnings(ctx context.Context, warns ...error) {
	uuid, ok := ctx.Value(warningsUUID{}).(string)

	if ok {
		warnings[uuid] = append(warnings[uuid], warns...)
	}
}

func ClearWarnings(ctx context.Context) {
	uuid, ok := ctx.Value(warningsUUID{}).(string)

	if ok {
		delete(warnings, uuid)
	}
}

func Warnings(ctx context.Context) []error {
	uuid, ok := ctx.Value(warningsUUID{}).(string)

	if ok {
		return warnings[uuid]
	}

	return nil
}
