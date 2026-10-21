package scorchexe

import "context"

type runIDKey struct{}
type executionKey struct{}

func ExecutionID(ctx context.Context) string {
	id, _ := ctx.Value(executionKey{}).(string)
	return id
}

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
