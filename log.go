package godotnet

import (
	"log/slog"
	"os"
)

// Logger is the structured-logging interface godotnet calls into.
// Methods take a message and zero or more key-value pairs in slog format
// (alternating keys and values, or *slog.Attr).
type Logger interface {
	Debug(msg string, kv ...any)
	Info(msg string, kv ...any)
	Warn(msg string, kv ...any)
	Error(msg string, kv ...any)
}

type slogLogger struct {
	l *slog.Logger
}

// NewDefaultLogger returns a Logger backed by slog. If h is nil, it defaults
// to a JSON handler writing to stderr at Info level.
func NewDefaultLogger(h slog.Handler) Logger {
	if h == nil {
		h = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	}
	return &slogLogger{l: slog.New(h)}
}

func (s *slogLogger) Debug(msg string, kv ...any) { s.l.Debug(msg, kv...) }
func (s *slogLogger) Info(msg string, kv ...any)  { s.l.Info(msg, kv...) }
func (s *slogLogger) Warn(msg string, kv ...any)  { s.l.Warn(msg, kv...) }
func (s *slogLogger) Error(msg string, kv ...any) { s.l.Error(msg, kv...) }

type nopLogger struct{}

func (nopLogger) Debug(string, ...any) {}
func (nopLogger) Info(string, ...any)  {}
func (nopLogger) Warn(string, ...any)  {}
func (nopLogger) Error(string, ...any) {}

// loggerOrNop returns logger, or a discarding Logger if logger is nil.
func loggerOrNop(logger Logger) Logger {
	if logger == nil {
		return nopLogger{}
	}
	return logger
}
