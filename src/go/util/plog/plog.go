package plog

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"

	"github.com/lmittmann/tint"

	"phenix/util/termcolor"
)

var (
	handler = &phenixHandler{ //nolint:gochecknoglobals,exhaustruct // package level logger
		handlers: make(map[string]slog.Handler),
	}
	logger = slog.New(handler) //nolint:gochecknoglobals // package level logger

	// Level is used to adjust log level of "phenix-default" handler only.
	Level = new(slog.LevelVar) //nolint:gochecknoglobals // package level logger

	// list of log attribute keys to remove from the default logger.
	logKeysIgnored = map[string]struct{}{ScorchSohKey: {}, auditTimestampKey: {}} //nolint:gochecknoglobals // package level logger
)

// main phenix [slog.Handler].
type phenixHandler struct {
	handlers map[string]slog.Handler

	mu sync.RWMutex
}

// NewPhenixHandler creates a new [slog.TextHandler] named "phenix-default"
// logging to STDERR. This handler will default to a log level of [slog.LevelInfo]
// until it is changed with the "SetLevelText" function. With color, it colors
// each level, the message of a warning yellow, and that of an error or alarm red.
func NewPhenixHandler(w io.Writer, color bool) {
	if w == nil {
		w = os.Stderr
	}

	options := &tint.Options{ //nolint:exhaustruct // partial initialization
		Level:      Level,
		TimeFormat: "2006-01-02 15:04:05.000",
		NoColor:    !color,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if _, ok := logKeysIgnored[a.Key]; ok {
				return slog.Attr{}
			}

			return a
		},
	}

	console := tint.NewTextHandler(w, options)
	if color {
		console = consoleColors{Handler: console, palette: termcolor.New(true)}
	}

	handler.AddHandler("phenix-default", console)
}

// ChangeConsoleLogger updates the "phenix-default" handler with a new writer,
// colored as NewPhenixHandler colors it.
func ChangeConsoleLogger(w io.Writer, color bool) {
	NewPhenixHandler(w, color)
}

// consoleColors colors the message of each record that warns or fails, as
// tint colors the level beside it.
type consoleColors struct {
	slog.Handler

	palette termcolor.Palette
}

// Handle colors the message of a warning yellow, and that of an error or an
// alarm red, then passes the record on.
func (h consoleColors) Handle(ctx context.Context, r slog.Record) error {
	switch {
	case r.Level >= slog.LevelError, r.Level >= slog.LevelWarn && isAlarm(ctx):
		r.Message = h.palette.Red(r.Message)
	case r.Level >= slog.LevelWarn:
		r.Message = h.palette.Yellow(r.Message)
	}

	return h.Handler.Handle(ctx, r)
}

// WithAttrs implements the [slog.Handler] interface for consoleColors.
func (h consoleColors) WithAttrs(attrs []slog.Attr) slog.Handler {
	return consoleColors{Handler: h.Handler.WithAttrs(attrs), palette: h.palette}
}

// WithGroup implements the [slog.Handler] interface for consoleColors.
func (h consoleColors) WithGroup(name string) slog.Handler {
	return consoleColors{Handler: h.Handler.WithGroup(name), palette: h.palette}
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
	if isAudit(ctx) {
		return true
	}

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

	for name, handler := range h.handlers {
		// an audit record reaches the system log file whatever its level
		if handler.Enabled(ctx, r.Level) || (name == fileHandlerName && isAudit(ctx)) {
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
