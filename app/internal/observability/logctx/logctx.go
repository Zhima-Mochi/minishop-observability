package logctx

import (
	"context"

	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability"
)

type loggerKey struct{}

// With stores the provided logger on the context for request-scoped logging.
func With(ctx context.Context, logger observability.Logger) context.Context {
	if ctx == nil || logger == nil {
		return ctx
	}
	return context.WithValue(ctx, loggerKey{}, logger)
}

// From retrieves a logger from the context if present.
func From(ctx context.Context) observability.Logger {
	if ctx == nil {
		return nil
	}
	logger, _ := ctx.Value(loggerKey{}).(observability.Logger)
	return logger
}

// FromOr returns the context logger when available, otherwise falls back to the supplied logger.
func FromOr(ctx context.Context, fallback observability.Logger) observability.Logger {
	if logger := From(ctx); logger != nil {
		return logger
	}
	return fallback
}
