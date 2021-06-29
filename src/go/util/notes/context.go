package notes

import (
	"context"
	"encoding/json"

	"github.com/fatih/color"
	"github.com/gofrs/uuid"
)

type noteUUID struct{}

var (
	errs     = make(map[string][]error)
	warnings = make(map[string][]error)
	info     = make(map[string][]string)
)

func Context(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}

	uuid := uuid.Must(uuid.NewV4()).String()
	return context.WithValue(ctx, noteUUID{}, uuid)
}

func AddErrors(ctx context.Context, e ...error) {
	uuid, ok := ctx.Value(noteUUID{}).(string)

	if ok {
		errs[uuid] = append(errs[uuid], e...)
	}
}

func AddWarnings(ctx context.Context, w ...error) {
	uuid, ok := ctx.Value(noteUUID{}).(string)

	if ok {
		warnings[uuid] = append(warnings[uuid], w...)
	}
}

func AddInfo(ctx context.Context, i ...string) {
	uuid, ok := ctx.Value(noteUUID{}).(string)

	if ok {
		info[uuid] = append(info[uuid], i...)
	}
}

func ClearErrors(ctx context.Context) {
	uuid, ok := ctx.Value(noteUUID{}).(string)

	if ok {
		delete(errs, uuid)
	}
}

func ClearWarnings(ctx context.Context) {
	uuid, ok := ctx.Value(noteUUID{}).(string)

	if ok {
		delete(warnings, uuid)
	}
}

func ClearInfo(ctx context.Context) {
	uuid, ok := ctx.Value(noteUUID{}).(string)

	if ok {
		delete(info, uuid)
	}
}

func Errors(ctx context.Context) []error {
	uuid, ok := ctx.Value(noteUUID{}).(string)

	if ok {
		return errs[uuid]
	}

	return nil
}

func Warnings(ctx context.Context) []error {
	uuid, ok := ctx.Value(noteUUID{}).(string)

	if ok {
		return warnings[uuid]
	}

	return nil
}

func Info(ctx context.Context) []string {
	uuid, ok := ctx.Value(noteUUID{}).(string)

	if ok {
		return info[uuid]
	}

	return nil
}

func PrettyPrint(ctx context.Context) {
	uuid, ok := ctx.Value(noteUUID{}).(string)

	if !ok {
		return
	}

	for _, e := range errs[uuid] {
		color.New(color.FgRed).Printf("[✗] %v\n", e)
	}

	for _, w := range warnings[uuid] {
		color.New(color.FgYellow).Printf("[?] %v\n", w)
	}

	for _, i := range info[uuid] {
		color.New(color.FgBlue).Printf("[✓] %s\n", i)
	}
}

func ToJSON(ctx context.Context) json.RawMessage {
	uuid, ok := ctx.Value(noteUUID{}).(string)

	if !ok {
		return nil
	}

	body := make(map[string][]string)

	if v, ok := errs[uuid]; ok {
		s := make([]string, len(v))

		for i, e := range v {
			s[i] = e.Error()
		}

		body["errors"] = s
	}

	if v, ok := warnings[uuid]; ok {
		s := make([]string, len(v))

		for i, e := range v {
			s[i] = e.Error()
		}

		body["warnings"] = s
	}

	if v, ok := info[uuid]; ok {
		body["warnings"] = v
	}

	raw, _ := json.Marshal(body)

	return raw
}
