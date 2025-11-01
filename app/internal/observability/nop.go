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
func NopTracer() Tracer { return nopTracer{} }

type nopMetrics struct{}

func (nopMetrics) Counter(MetricKey) Counter     { return nopCounter{} }
func (nopMetrics) Histogram(MetricKey) Histogram { return nopHistogram{} }

// NopMetrics returns a metrics provider whose instruments drop all observations.
func NopMetrics() Metrics { return nopMetrics{} }

type nopCounter struct{}

func (nopCounter) Add(_ float64, _ ...Label)    {}
func (nopCounter) Bind(_ ...Label) BoundCounter { return nopBoundCounter{} }

func NopCounter() Counter { return nopCounter{} }

type nopBoundCounter struct{}

func (nopBoundCounter) Add(_ float64) {}

type nopHistogram struct{}

func (nopHistogram) Observe(_ float64, _ ...Label)  {}
func (nopHistogram) Bind(_ ...Label) BoundHistogram { return nopBoundHistogram{} }

func NopHistogram() Histogram { return nopHistogram{} }

type nopBoundHistogram struct{}

func (nopBoundHistogram) Observe(_ float64) {}
