package scorchexe

import "context"

type runIDKey struct{}

func MustRunID(ctx context.Context) int {
	id := ctx.Value(runIDKey{}).(int)
	return id
}

func RunID(ctx context.Context) (int, bool) {
	id, ok := ctx.Value(runIDKey{}).(int)
	return id, ok
}

func SetRunID(ctx context.Context, id int) context.Context {
	return context.WithValue(ctx, runIDKey{}, id)
}
