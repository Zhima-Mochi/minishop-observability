package zaplogger

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type logger struct{ l *zap.Logger }

func New(fixed ...observability.Field) observability.Logger {
	cfg := zap.NewProductionConfig()
	cfg.OutputPaths = []string{"stdout"}
	cfg.ErrorOutputPaths = []string{"stdout"}

	if logFile := os.Getenv("LOG_FILE"); logFile != "" {
		if err := ensureLogFile(logFile); err != nil {
			panic(fmt.Errorf("prepare log file: %w", err))
		}
		cfg.OutputPaths = append(cfg.OutputPaths, logFile)
		cfg.ErrorOutputPaths = append(cfg.ErrorOutputPaths, logFile)
	}

	// Ensure encoder keys align with structured logging requirements.
	cfg.EncoderConfig.TimeKey = "ts"
	cfg.EncoderConfig.MessageKey = "msg"
	cfg.EncoderConfig.EncodeTime = zapcore.RFC3339NanoTimeEncoder
	cfg.EncoderConfig.EncodeLevel = zapcore.LowercaseLevelEncoder

	cfg.InitialFields = map[string]any{}
	for _, f := range fixed {
		cfg.InitialFields[f.Key] = f.Value
	}

	l, err := cfg.Build()
	if err != nil {
		panic(err)
	}
	return &logger{l: l}
}

func (z *logger) With(fields ...observability.Field) observability.Logger {
	if len(fields) == 0 {
		return &logger{l: z.l}
	}
	return &logger{l: z.l.With(toZapFields(fields)...)}
}

func (z *logger) Debug(msg string, fields ...observability.Field) {
	z.l.Debug(msg, toZapFields(fields)...)
}
func (z *logger) Info(msg string, fields ...observability.Field) {
	z.l.Info(msg, toZapFields(fields)...)
}
func (z *logger) Warn(msg string, fields ...observability.Field) {
	z.l.Warn(msg, toZapFields(fields)...)
}
func (z *logger) Error(msg string, fields ...observability.Field) {
	z.l.Error(msg, toZapFields(fields)...)
}

// Sync flushes any buffered log entries. Safe to call on shutdown.
func (z *logger) Sync() error {
	return z.l.Sync()
}

func toZapFields(fs []observability.Field) []zap.Field {
	out := make([]zap.Field, 0, len(fs))
	for _, f := range fs {
		out = append(out, zap.Any(f.Key, f.Value))
	}
	return out
}

func ensureLogFile(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		f, createErr := os.OpenFile(path, os.O_CREATE, 0o644)
		if createErr != nil {
			return createErr
		}
		_ = f.Close()
	}
	return nil
}
