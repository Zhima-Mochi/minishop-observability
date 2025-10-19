package logging

import (
	"context"

	"go.uber.org/zap"
)

type ctxKey struct{}

// ContextWithLogger stores the given logger in the context.
func ContextWithLogger(ctx context.Context, logger *zap.Logger) context.Context {
	if logger == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, logger)
}

// FromContext retrieves a logger from the context, falling back to zap.L().
func FromContext(ctx context.Context) *zap.Logger {
	if ctx == nil {
		return zap.L()
	}
	if logger, ok := ctx.Value(ctxKey{}).(*zap.Logger); ok && logger != nil {
		return logger
	}
	return zap.L()
}
