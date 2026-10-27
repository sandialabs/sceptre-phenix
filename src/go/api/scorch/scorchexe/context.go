package scorchexe

import "context"

type runIDKey struct{}

func MustRunID(ctx context.Context) int {
	id, _ := ctx.Value(runIDKey{}).(int)

	return id
}

func SetRunID(ctx context.Context, id int) context.Context {
	return context.WithValue(ctx, runIDKey{}, id)
}
