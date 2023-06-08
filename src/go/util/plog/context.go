package plog

import (
	"context"

	"golang.org/x/exp/slog"
)

type plogKey struct{}

func ContextWithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, plogKey{}, l)
}

func LoggerFromContext(ctx context.Context) *slog.Logger {
	l, ok := ctx.Value(plogKey{}).(*slog.Logger)
	if !ok {
		return logger // return default package logger
	}

	return l
}
