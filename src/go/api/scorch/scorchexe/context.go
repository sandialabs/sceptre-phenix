package scorchexe

import "context"

type runIDKey struct{}

func MustRunID(ctx context.Context) int {
	id, _ := ctx.Value(runIDKey{}).(int)

	return id
}

func RunID(ctx context.Context) (int, bool) {
	id, ok := ctx.Value(runIDKey{}).(int)

	return id, ok
}

func SetRunID(ctx context.Context, id int) context.Context {
	return context.WithValue(ctx, runIDKey{}, id)
}

type cleanupOnlyKey struct{}

// CleanupOnly reports whether the run should execute only its cleanup stage.
func CleanupOnly(ctx context.Context) bool {
	only, _ := ctx.Value(cleanupOnlyKey{}).(bool)

	return only
}

// SetCleanupOnly marks the run to execute only its cleanup stage, skipping
// every other stage, its loops, and its data collection.
func SetCleanupOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, cleanupOnlyKey{}, true)
}
