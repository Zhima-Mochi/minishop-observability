package observability

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type nopLogger struct{}

func (nopLogger) With(_ ...Field) Logger { return nopLogger{} }
func (nopLogger) Debug(string, ...Field) {}
func (nopLogger) Info(string, ...Field)  {}
func (nopLogger) Warn(string, ...Field)  {}
func (nopLogger) Error(string, ...Field) {}

// NopLogger returns a logger that discards all logs. Useful as a safe fallback.
func NopLogger() Logger { return nopLogger{} }

type nopTracer struct{}

func (nopTracer) Start(ctx context.Context, _ string, _ ...attribute.KeyValue) (context.Context, trace.Span) {
	return ctx, trace.SpanFromContext(ctx)
}

// NopTracer returns a tracer that simply propagates the existing span from the context.
func NopTracer() TraceCtx { return nopTracer{} }
