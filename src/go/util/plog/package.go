package plog

import (
	"context"
	"log/slog"
	"strings"
)

// SetLevel sets the log level of the "phenix-default" [slog.Handler].
func SetLevel(l slog.Level) {
	Level.Set(l)
}

// SetLevelText sets the log level of the "phenix-default" [slog.Handler].
func SetLevelText(l string) {
	if strings.ToUpper(l) == "NONE" {
		Level.Set(slog.LevelError + 1)

		return
	}

	_ = Level.UnmarshalText([]byte(l))
}

func SetLogFile(path string) {
	ChangeLogFile(path)
}

func TextToLevel(level string) slog.Level {
	switch strings.ToUpper(level) {
	case "NONE":
		return slog.LevelError + 1
	case "WARNING": // python using warning rather than warn
		return slog.LevelWarn
	default:
		var l slog.Level

		err := l.UnmarshalText([]byte(level))
		if err != nil {
			l = slog.LevelInfo
		}

		return l
	}
}

// AddHandler adds a new [slog.Handler] by name to the main phenix [slog.Handler].
func AddHandler(name string, h slog.Handler) {
	if handler == nil {
		return
	}

	handler.AddHandler(name, h)
}

// RemoveHandler removes the named [slog.Handler] from the main phenix [slog.Handler].
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

type LogType string

// Keep in sync with Log.vue.
const (
	TypeSecurity  LogType = "SECURITY"
	TypeSoh       LogType = "SOH"
	TypeScorch    LogType = "SCORCH"
	TypePhenixApp LogType = "PHENIX-APP"
	TypeAction    LogType = "ACTION"
	TypeHTTP      LogType = "HTTP"
	TypeMinimega  LogType = "MINIMEGA"
	TypeSystem    LogType = "SYSTEM" // default. Use if no other option is appropriate
)

// Log logs to configured loggers. LogType is a required enum used for identifying what a log message is for.
func Log(l slog.Level, t LogType, msg string, args ...any) {
	if logger == nil {
		return
	}

	logger.Log(context.Background(), l, msg, append([]any{"type", t}, args...)...)
}

func Debug(t LogType, msg string, args ...any) {
	Log(slog.LevelDebug, t, msg, args...)
}

func Info(t LogType, msg string, args ...any) {
	Log(slog.LevelInfo, t, msg, args...)
}

func Warn(t LogType, msg string, args ...any) {
	Log(slog.LevelWarn, t, msg, args...)
}

func Error(t LogType, msg string, args ...any) {
	Log(slog.LevelError, t, msg, args...)
}
