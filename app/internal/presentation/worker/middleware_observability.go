package workerpresentation

import (
	"context"

	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability/logctx"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
)

// WithEventContext injects a request-scoped logger for background/worker executions.
// Dynamic fields only: trace_id/span_id (if valid), event_id (generated if empty),
// plus caller-provided low-cardinality attributes (e.g. "use_case", "event", "tenant_id").
func WithEventContext(
	ctx context.Context,
	base observability.Logger,
	tel observability.Observability,
	traceID trace.TraceID,
	spanID trace.SpanID,
	attrs map[string]string, // keep this low-cardinality: event name, tenant, shard, queue, etc.
) context.Context {
	if base == nil {
		base = tel.Logger()
	}

	if attrs == nil {
		attrs = make(map[string]string)
	}

	fields := make([]observability.Field, 0, 6)

	// Prefer a stable, human-pivotable ID for the event
	evtID := attrs["event_id"]
	if evtID == "" {
		evtID = uuid.NewString()
	}
	fields = append(fields, observability.F("event_id", evtID))

	// Add trace identifiers only if they are valid
	if traceID.IsValid() {
		fields = append(fields, observability.F("trace_id", traceID.String()))
	}
	if spanID.IsValid() {
		fields = append(fields, observability.F("span_id", spanID.String()))
	}

	// Copy over remaining attributes (skip event_id since we already normalized it)
	for k, v := range attrs {
		if k == "event_id" || v == "" {
			continue
		}
		fields = append(fields, observability.F(k, v))
	}

	reqLogger := base.With(fields...)
	return logctx.With(ctx, reqLogger)
}
