package app

import "context"

type (
	TriggerUI  struct{}
	TriggerCLI struct{}
)

func SetContextTriggerUI(ctx context.Context) context.Context {
	return context.WithValue(ctx, TriggerUI{}, struct{}{})
}

func SetContextTriggerCLI(ctx context.Context) context.Context {
	return context.WithValue(ctx, TriggerCLI{}, struct{}{})
}

func IsContextTriggerUI(ctx context.Context) bool {
	ok := ctx.Value(TriggerUI{})
	return ok != nil
}

func IsContextTriggerCLI(ctx context.Context) bool {
	ok := ctx.Value(TriggerCLI{})
	return ok != nil
}
