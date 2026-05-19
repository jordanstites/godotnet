package godotnet

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestLoggerOrNop_NilReturnsNopAndDoesNotPanic(t *testing.T) {
	l := loggerOrNop(nil)
	if l == nil {
		t.Fatal("loggerOrNop(nil) returned nil")
	}
	l.Debug("d")
	l.Info("i", "k", 1)
	l.Warn("w", "k", "v")
	l.Error("e", "err", "x")
}

func TestLoggerOrNop_NonNilReturnsSame(t *testing.T) {
	in := NewDefaultLogger(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	out := loggerOrNop(in)
	if in != out {
		t.Fatal("loggerOrNop returned a different logger than its input")
	}
}

func TestNewDefaultLogger_EmitsStructuredJSON(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	log := NewDefaultLogger(h)

	log.Info("hello", "player", 42)

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("default logger produced no output")
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("output is not JSON: %v\nline: %s", err, line)
	}
	if m["msg"] != "hello" {
		t.Errorf("msg: got %v, want hello", m["msg"])
	}
	if m["player"] != float64(42) {
		t.Errorf("player: got %v, want 42", m["player"])
	}
	if m["level"] != "INFO" {
		t.Errorf("level: got %v, want INFO", m["level"])
	}
}

func TestNewDefaultLogger_NilHandlerStillReturnsLogger(t *testing.T) {
	log := NewDefaultLogger(nil)
	if log == nil {
		t.Fatal("NewDefaultLogger(nil) returned nil")
	}
}

func TestNopLogger_DoesNotPanic(t *testing.T) {
	var l Logger = nopLogger{}
	l.Debug("d", "k", 1)
	l.Info("i", "k", 1)
	l.Warn("w", "k", 1)
	l.Error("e", "k", 1)
}
