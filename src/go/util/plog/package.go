package plog

import "golang.org/x/exp/slog"

// SetLevel sets the log level of the "phenix-default" slog.Handler.
func SetLevel(l slog.Level) {
	level.Set(l)
}

// SetLevelText sets the log level of the "phenix-default" slog.Handler.
func SetLevelText(l string) {
	level.UnmarshalText([]byte(l))
}

// AddHandler adds a new slog.Handler by name to the main phenix slog.Handler.
func AddHandler(name string, h slog.Handler) {
	if handler == nil {
		return
	}

	handler.AddHandler(name, h)
}

// RemoveHandler removes the named slog.Handler from the main phenix slog.Handler.
func RemoveHandler(name string) {
	if handler == nil {
		return
	}

	handler.RemoveHandler(name)
}

func With(args ...any) *slog.Logger {
	if logger == nil {
		return nil
	}

	return logger.With(args...)
}

func Debug(msg string, args ...any) {
	if logger == nil {
		return
	}

	logger.Debug(msg, args...)
}

func Info(msg string, args ...any) {
	if logger == nil {
		return
	}

	logger.Info(msg, args...)
}

func Warn(msg string, args ...any) {
	if logger == nil {
		return
	}

	logger.Warn(msg, args...)
}

func Error(msg string, args ...any) {
	if logger == nil {
		return
	}

	logger.Error(msg, args...)
}
