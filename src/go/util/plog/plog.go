package plog

import (
	"context"
	"errors"
	"os"
	"sync"

	"github.com/lmittmann/tint"
	"github.com/mattn/go-isatty"
	"golang.org/x/exp/slog"
)

var (
	handler = &phenixHandler{handlers: make(map[string]slog.Handler)}
	logger  = slog.New(handler)

	// used to adjust log level of "phenix-default" handler only
	level = new(slog.LevelVar)

	// list of log attribute keys to remove from the default logger
	ignore = map[string]struct{}{ScorchSohKey: {}}
)

// main phenix slog.Handler
type phenixHandler struct {
	handlers map[string]slog.Handler

	mu sync.RWMutex
}

// NewPhenixHandler creates a new slog.TextHandler named "phenix-default"
// logging to STDERR. This handler will default to a log level of slog.LevelInfo
// until it is changed with the "SetLevel" function.
func NewPhenixHandler() {
	// options := &slog.HandlerOptions{
	options := &tint.Options{
		Level:   level,
		NoColor: !isatty.IsTerminal(os.Stderr.Fd()),
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if _, ok := ignore[a.Key]; ok {
				return slog.Attr{}
			}

			return a
		},
	}

	// handler.AddHandler("phenix-default", slog.NewTextHandler(os.Stderr, options))
	handler.AddHandler("phenix-default", tint.NewHandler(os.Stderr, options))
}

// AddHandler adds a new slog.Handler by name to the main phenix slog.Handler.
func (this *phenixHandler) AddHandler(name string, h slog.Handler) {
	this.mu.Lock()
	defer this.mu.Unlock()

	this.handlers[name] = h
}

// RemoveHandler removes the named slog.Handler from the main phenix slog.Handler.
func (this *phenixHandler) RemoveHandler(name string) {
	this.mu.Lock()
	defer this.mu.Unlock()

	delete(this.handlers, name)
}

// Enabled implements the slog.Handler interface for the phenix handler.
func (this *phenixHandler) Enabled(ctx context.Context, l slog.Level) bool {
	this.mu.RLock()
	defer this.mu.RUnlock()

	for _, h := range this.handlers {
		if h.Enabled(ctx, l) {
			return true
		}
	}

	return false
}

// Handle implements the slog.Handler interface for the phenix handler.
func (this *phenixHandler) Handle(ctx context.Context, r slog.Record) error {
	this.mu.RLock()
	defer this.mu.RUnlock()

	var errs error

	for _, h := range this.handlers {
		if h.Enabled(ctx, r.Level) {
			errs = errors.Join(errs, h.Handle(ctx, r.Clone()))
		}
	}

	return errs
}

// WithAttrs implements the slog.Handler interface for the phenix handler.
func (this *phenixHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	this.mu.RLock()
	defer this.mu.RUnlock()

	with := make(map[string]slog.Handler)

	for n, h := range this.handlers {
		with[n] = h.WithAttrs(attrs)
	}

	return &phenixHandler{handlers: with}
}

// WithGroup implements the slog.Handler interface for the phenix handler.
func (this *phenixHandler) WithGroup(name string) slog.Handler {
	this.mu.RLock()
	defer this.mu.RUnlock()

	with := make(map[string]slog.Handler)

	for n, h := range this.handlers {
		with[n] = h.WithGroup(name)
	}

	return &phenixHandler{handlers: with}
}
