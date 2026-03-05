package plog

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"

	"github.com/lmittmann/tint"
	"github.com/mattn/go-isatty"
)

var (
	handler = &phenixHandler{ //nolint:gochecknoglobals,exhaustruct // package level logger
		handlers: make(map[string]slog.Handler),
	}
	logger = slog.New(handler) //nolint:gochecknoglobals // package level logger

	// Level is used to adjust log level of "phenix-default" handler only.
	Level = new(slog.LevelVar) //nolint:gochecknoglobals // package level logger

	// list of log attribute keys to remove from the default logger.
	logKeysIgnored = map[string]struct{}{ScorchSohKey: {}} //nolint:gochecknoglobals // package level logger
)

// main phenix [slog.Handler].
type phenixHandler struct {
	handlers map[string]slog.Handler

	mu sync.RWMutex
}

// NewPhenixHandler creates a new [slog.TextHandler] named "phenix-default"
// logging to STDERR. This handler will default to a log level of [slog.LevelInfo]
// until it is changed with the "SetLevel" function.
func NewPhenixHandler(w io.Writer) {
	if w == nil {
		w = os.Stderr
	}

	noColor := true
	if f, ok := w.(*os.File); ok {
		noColor = !isatty.IsTerminal(f.Fd())
	}

	options := &tint.Options{ //nolint:exhaustruct // partial initialization
		Level:      Level,
		TimeFormat: "2006-01-02 15:04:05.000",
		NoColor:    noColor,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if _, ok := logKeysIgnored[a.Key]; ok {
				return slog.Attr{}
			}

			return a
		},
	}

	handler.AddHandler("phenix-default", tint.NewHandler(w, options))
}

// ChangeConsoleLogger updates the "phenix-default" handler with a new writer.
func ChangeConsoleLogger(w io.Writer) {
	NewPhenixHandler(w)
}

// AddHandler adds a new [slog.Handler] by name to the main phenix [slog.Handler].
func (h *phenixHandler) AddHandler(name string, handler slog.Handler) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.handlers[name] = handler
}

// RemoveHandler removes the named [slog.Handler] from the main phenix [slog.Handler].
func (h *phenixHandler) RemoveHandler(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.handlers, name)
}

// Enabled implements the [slog.Handler] interface for the phenix handler.
func (h *phenixHandler) Enabled(ctx context.Context, l slog.Level) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, handler := range h.handlers {
		if handler.Enabled(ctx, l) {
			return true
		}
	}

	return false
}

// Handle implements the [slog.Handler] interface for the phenix handler.
func (h *phenixHandler) Handle(ctx context.Context, r slog.Record) error {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var errs error

	for _, handler := range h.handlers {
		if handler.Enabled(ctx, r.Level) {
			errs = errors.Join(errs, handler.Handle(ctx, r.Clone()))
		}
	}

	return errs
}

// WithAttrs implements the [slog.Handler] interface for the phenix handler.
func (h *phenixHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.mu.RLock()
	defer h.mu.RUnlock()

	with := make(map[string]slog.Handler)

	for n, handler := range h.handlers {
		with[n] = handler.WithAttrs(attrs)
	}

	return &phenixHandler{handlers: with} //nolint:exhaustruct // partial initialization
}

// WithGroup implements the [slog.Handler] interface for the phenix handler.
func (h *phenixHandler) WithGroup(name string) slog.Handler {
	h.mu.RLock()
	defer h.mu.RUnlock()

	with := make(map[string]slog.Handler)

	for n, handler := range h.handlers {
		with[n] = handler.WithGroup(name)
	}

	return &phenixHandler{handlers: with} //nolint:exhaustruct // partial initialization
}
