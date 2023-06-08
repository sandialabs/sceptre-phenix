package plog

import (
	"context"
	"fmt"
	"strings"
	"time"

	"golang.org/x/exp/slog"
)

type uiHandler struct {
	level    slog.Level
	attrs    []slog.Attr
	callback func(time.Time, string, string)
}

func NewUIHandler(level string, cb func(time.Time, string, string)) slog.Handler {
	var l slog.Level

	if err := l.UnmarshalText([]byte(level)); err != nil {
		l = slog.LevelInfo
	}

	return &uiHandler{
		level:    l,
		callback: cb,
	}
}

// Enabled implements the slog.Handler interface for the ui handler.
func (this uiHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= this.level
}

// Handle implements the slog.Handler interface for the ui handler.
func (this uiHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := []string{r.Message}

	for _, attr := range this.attrs {
		if _, ok := ignore[attr.Key]; !ok {
			attrs = append(attrs, fmt.Sprintf("%s=%v", attr.Key, attr.Value.Any()))
		}
	}

	r.Attrs(func(attr slog.Attr) bool {
		if _, ok := ignore[attr.Key]; !ok {
			attrs = append(attrs, fmt.Sprintf("%s=%v", attr.Key, attr.Value.Any()))
		}

		return true
	})

	this.callback(r.Time.UTC(), r.Level.String(), strings.Join(attrs, " "))

	return nil
}

// WithAttrs implements the slog.Handler interface for the ui handler.
func (this *uiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &uiHandler{
		level:    this.level,
		attrs:    append(this.attrs, attrs...),
		callback: this.callback,
	}
}

// WithGroup implements the slog.Handler interface for the ui handler. This
// function is currently not implemented, and instead simply returns this same
// handler.
func (this *uiHandler) WithGroup(name string) slog.Handler {
	return this
}
