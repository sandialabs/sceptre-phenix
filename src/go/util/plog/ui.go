package plog

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

type uiHandler struct {
	level    slog.Level
	attrs    []slog.Attr
	callback func(time time.Time, level, logtype, message string)
}

func NewUIHandler(
	level string,
	cb func(time time.Time, level, logtype, message string),
) slog.Handler {
	var l slog.Level

	err := l.UnmarshalText([]byte(level))
	if err != nil {
		l = slog.LevelInfo
	}

	return &uiHandler{ //nolint:exhaustruct // partial initialization
		level:    l,
		callback: cb,
	}
}

// Enabled implements the [slog.Handler] interface for the ui handler.
func (h uiHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level
}

// Handle implements the [slog.Handler] interface for the ui handler.
func (h uiHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := []string{r.Message}
	logtype := string(TypeSystem)

	for _, attr := range h.attrs {
		if _, ok := logKeysIgnored[attr.Key]; !ok {
			attrs = append(attrs, fmt.Sprintf("%s=%v", attr.Key, attr.Value.Any()))
		}
	}

	r.Attrs(func(attr slog.Attr) bool {
		if attr.Key == "type" {
			logtype = attr.Value.String()
		} else if _, ok := logKeysIgnored[attr.Key]; !ok {
			attrs = append(attrs, fmt.Sprintf("%s=%v", attr.Key, attr.Value.Any()))
		}

		return true
	})

	h.callback(r.Time.UTC(), r.Level.String(), logtype, strings.Join(attrs, " "))

	return nil
}

// WithAttrs implements the [slog.Handler] interface for the ui handler.
func (h *uiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &uiHandler{
		level:    h.level,
		attrs:    append(h.attrs, attrs...),
		callback: h.callback,
	}
}

// WithGroup implements the [slog.Handler] interface for the ui handler. This
// function is currently not implemented, and instead simply returns this same
// handler.
func (h *uiHandler) WithGroup(name string) slog.Handler {
	return h
}
