package observability

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type Observability interface {
	Tracer() Tracer
	Logger() Logger
	Metrics() Metrics
}

type Metrics interface {
	Counter(name MetricKey) Counter
	Histogram(name MetricKey) Histogram
}

// Tracer is a thin wrapper to start spans.
type Tracer interface {
	Start(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span)
}

// Counter is a thin wrapper to add metrics.
type Counter interface {
	Add(delta float64, labels ...Label)
	Bind(labels ...Label) BoundCounter
}

type BoundCounter interface {
	Add(delta float64)
}

type Histogram interface {
	Observe(value float64, labels ...Label)
	Bind(labels ...Label) BoundHistogram
}

type BoundHistogram interface {
	Observe(value float64)
}

type Label struct{ Key, Value string }

func L(k, v string) Label { return Label{Key: k, Value: v} }

type Field struct {
	Key   string
	Value any
}

func F(k string, v any) Field { return Field{Key: k, Value: v} }

// Logger is a thin wrapper to log messages.
type Logger interface {
	With(fields ...Field) Logger
	Debug(msg string, fields ...Field)
	Info(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Error(msg string, fields ...Field)
}

type MetricKey string
