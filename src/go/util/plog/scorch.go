package plog

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

const ScorchSohKey = "__soh-id__"

type scorchSohHandler struct {
	name     string
	level    slog.Level
	attrs    []slog.Attr
	callback func(string)
}

func NewScorchSohHandler(name, level string, cb func(string)) slog.Handler {
	var l slog.Level

	err := l.UnmarshalText([]byte(level))
	if err != nil {
		l = slog.LevelInfo
	}

	return &scorchSohHandler{ //nolint:exhaustruct // partial initialization
		name:     name,
		level:    l,
		callback: cb,
	}
}

// Enabled implements the [slog.Handler] interface for the soh handler.
func (h scorchSohHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level
}

// Handle implements the [slog.Handler] interface for the soh handler.
func (h scorchSohHandler) Handle(_ context.Context, r slog.Record) error {
	var (
		attrs  = []string{r.Message}
		update bool
	)

	for _, attr := range h.attrs {
		if attr.Key == ScorchSohKey && attr.Value.String() == h.name {
			update = true
		} else {
			attrs = append(attrs, fmt.Sprintf("%s=%v", attr.Key, attr.Value.Any()))
		}
	}

	r.Attrs(func(attr slog.Attr) bool {
		if attr.Key == ScorchSohKey && attr.Value.String() == h.name {
			update = true
		} else {
			attrs = append(attrs, fmt.Sprintf("%s=%v", attr.Key, attr.Value.Any()))
		}

		return true
	})

	if update {
		output := fmt.Sprintf(
			"[%s] %s\n",
			r.Time.UTC().Format(TimestampFormat),
			strings.Join(attrs, " "),
		)
		h.callback(output)
	}

	return nil
}

// WithAttrs implements the [slog.Handler] interface for the soh handler.
func (h *scorchSohHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &scorchSohHandler{
		name:     h.name,
		level:    h.level,
		attrs:    append(h.attrs, attrs...),
		callback: h.callback,
	}
}

// WithGroup implements the [slog.Handler] interface for the soh handler. This
// function is currently not implemented, and instead simply returns this same
// handler.
func (h *scorchSohHandler) WithGroup(name string) slog.Handler {
	return h
}
