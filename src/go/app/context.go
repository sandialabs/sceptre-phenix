package app

import (
	"context"
	"maps"
)

type (
	metadata   struct{}
	triggerUI  struct{}
	triggerCLI struct{}
)

// AddContextMetadata returns a child of ctx with key set to val in a copy of
// the metadata of ctx. The metadata of ctx does not change.
func AddContextMetadata(ctx context.Context, key string, val any) context.Context {
	md := make(map[string]any)

	if parent, ok := ctx.Value(metadata{}).(map[string]any); ok {
		maps.Copy(md, parent)
	}

	md[key] = val

	return context.WithValue(ctx, metadata{}, md)
}

func SetContextMetadata(ctx context.Context, md map[string]any) context.Context {
	return context.WithValue(ctx, metadata{}, md)
}

func SetContextTriggerUI(ctx context.Context) context.Context {
	return context.WithValue(ctx, triggerUI{}, struct{}{})
}

func SetContextTriggerCLI(ctx context.Context) context.Context {
	return context.WithValue(ctx, triggerCLI{}, struct{}{})
}

func GetContextMetadata(ctx context.Context) map[string]any {
	md := ctx.Value(metadata{})
	if md != nil {
		m, _ := md.(map[string]any)
		return m
	}

	return make(map[string]any)
}

func IsContextTriggerUI(ctx context.Context) bool {
	ok := ctx.Value(triggerUI{})

	return ok != nil
}

func IsContextTriggerCLI(ctx context.Context) bool {
	ok := ctx.Value(triggerCLI{})

	return ok != nil
}
