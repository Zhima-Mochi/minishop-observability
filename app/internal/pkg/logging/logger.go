package logging

import (
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	// SystemTraceID is used when no distributed trace context is available.
	SystemTraceID = "system"
	// SystemSpanID is used when no distributed span context is available.
	SystemSpanID = "system"
)

// NewLogger creates a production-ready zap logger that emits JSON logs to stdout.
// It enriches each log entry with the provided service and environment identifiers.
// When LOG_FILE is defined, logs are also duplicated to that file to aid local debugging.
func NewLogger(service, env string) (*zap.Logger, error) {
	cfg := zap.NewProductionConfig()
	cfg.OutputPaths = []string{"stdout"}
	cfg.ErrorOutputPaths = []string{"stdout"}

	if logFile := os.Getenv("LOG_FILE"); logFile != "" {
		if err := ensureLogFile(logFile); err != nil {
			return nil, fmt.Errorf("prepare log file: %w", err)
		}
		cfg.OutputPaths = append(cfg.OutputPaths, logFile)
		cfg.ErrorOutputPaths = append(cfg.ErrorOutputPaths, logFile)
	}

	// Ensure encoder keys align with structured logging requirements.
	cfg.EncoderConfig.TimeKey = "ts"
	cfg.EncoderConfig.MessageKey = "msg"
	cfg.EncoderConfig.EncodeTime = zapcore.RFC3339NanoTimeEncoder
	cfg.EncoderConfig.EncodeLevel = zapcore.LowercaseLevelEncoder

	cfg.InitialFields = map[string]any{
		"service": service,
		"env":     env,
	}

	return cfg.Build()
}

// MustNewLogger is like NewLogger but panics if the logger cannot be created.
func MustNewLogger(service, env string) *zap.Logger {
	logger, err := NewLogger(service, env)
	if err != nil {
		panic(err)
	}
	return logger
}

// WithTrace returns a logger enriched with trace and span identifiers.
// Unknown values are normalised to the literal "unknown" to ensure required fields exist.
func WithTrace(logger *zap.Logger, traceID, spanID string) *zap.Logger {
	if logger == nil {
		logger = zap.L()
	}
	if traceID == "" {
		traceID = "unknown"
	}
	if spanID == "" {
		spanID = "unknown"
	}
	return logger.With(
		zap.String("trace_id", traceID),
		zap.String("span_id", spanID),
	)
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
