package observability

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// TraceCtx is a thin wrapper to start spans without binding to a concrete tracer.
type TraceCtx interface {
	Start(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span)
}

// Minimal metric ports; hide prometheus types behind interfaces.
type Counter interface {
	Add(delta float64, labels ...Label)
}
type Histogram interface {
	Observe(value float64, labels ...Label)
}

type Label struct{ Key, Value string }

func L(k, v string) Label { return Label{Key: k, Value: v} }

type Field struct {
	Key   string
	Value any
}

func F(k string, v any) Field { return Field{Key: k, Value: v} }

// Logger interface
type Logger interface {
	With(fields ...Field) Logger
	Debug(msg string, fields ...Field)
	Info(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Error(msg string, fields ...Field)
}

type Telemetry interface {
	Tracer() TraceCtx
	Counter(name string) Counter
	Histogram(name string) Histogram
	Logger() Logger
}
