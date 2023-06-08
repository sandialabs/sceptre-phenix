package plog

import (
	"context"
	"fmt"
	"strings"
	"time"

	"golang.org/x/exp/slog"
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

	if err := l.UnmarshalText([]byte(level)); err != nil {
		l = slog.LevelInfo
	}

	return &scorchSohHandler{
		name:     name,
		level:    l,
		callback: cb,
	}
}

// Enabled implements the slog.Handler interface for the soh handler.
func (this scorchSohHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= this.level
}

// Handle implements the slog.Handler interface for the soh handler.
func (this scorchSohHandler) Handle(_ context.Context, r slog.Record) error {
	var (
		attrs  = []string{r.Message}
		update bool
	)

	for _, attr := range this.attrs {
		if attr.Key == ScorchSohKey && attr.Value.String() == this.name {
			update = true
		} else {
			attrs = append(attrs, fmt.Sprintf("%s=%v", attr.Key, attr.Value.Any()))
		}
	}

	r.Attrs(func(attr slog.Attr) bool {
		if attr.Key == ScorchSohKey && attr.Value.String() == this.name {
			update = true
		} else {
			attrs = append(attrs, fmt.Sprintf("%s=%v", attr.Key, attr.Value.Any()))
		}

		return true
	})

	if update {
		output := fmt.Sprintf("[%s] %s\n", r.Time.UTC().Format(time.DateTime), strings.Join(attrs, " "))
		this.callback(output)
	}

	return nil
}

// WithAttrs implements the slog.Handler interface for the soh handler.
func (this *scorchSohHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &scorchSohHandler{
		name:     this.name,
		level:    this.level,
		attrs:    append(this.attrs, attrs...),
		callback: this.callback,
	}
}

// WithGroup implements the slog.Handler interface for the soh handler. This
// function is currently not implemented, and instead simply returns this same
// handler.
func (this *scorchSohHandler) WithGroup(name string) slog.Handler {
	return this
}
