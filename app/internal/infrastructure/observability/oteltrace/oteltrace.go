package oteltrace

import (
	"context"

	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type tracer struct{ t trace.Tracer }

func New(name string) observability.TraceCtx {
	if name == "" {
		name = "minishop"
	}
	return &tracer{t: otel.Tracer(name)}
}

func (t *tracer) Start(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return t.t.Start(ctx, name, trace.WithAttributes(attrs...))
}

// you need to initialize sdktrace.TracerProvider + exporter, then set otel.SetTracerProvider(tp)
