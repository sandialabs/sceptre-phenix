package app

import "context"

type (
	metadata   struct{}
	triggerUI  struct{}
	triggerCLI struct{}
)

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
		return md.(map[string]any)
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
