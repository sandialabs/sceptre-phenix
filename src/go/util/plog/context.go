package plog

import (
	"context"
	"log/slog"
)

type plogKey struct{}

func ContextWithLogger(ctx context.Context, l *slog.Logger, t LogType) context.Context {
	return context.WithValue(ctx, plogKey{}, l.With("type", t))
}

func LoggerFromContext(ctx context.Context, t LogType) *slog.Logger {
	l, ok := ctx.Value(plogKey{}).(*slog.Logger)
	if !ok {
		return logger.With("type", t) //nolint:sloglint // return default package logger
	}

	return l.With("type", t)
}
