// internal/adapter/http/httppresentation/observability_mw.go
package httppresentation

import (
	"net/http"
	"time"

	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability/logctx"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// ObservabilityMiddleware combines:
// - W3C Trace Context extraction
// - request-scoped logger injection (dynamic fields only)
// - X-Request-ID generation + echo
// - HTTP metrics (counter + histogram) with low-cardinality labels
func ObservabilityMiddleware(
	base observability.Logger,
	requestID func(*http.Request) string,
	tenantID func(*http.Request) string,
	tel observability.Observability,
) func(http.Handler) http.Handler {
	if base == nil {
		if tel != nil {
			base = tel.Logger()
		} else {
			base = observability.NopLogger()
		}
	}
	prop := otel.GetTextMapPropagator() // W3C by default
	reqCounter := observability.NopCounter()
	reqHistogram := observability.NopHistogram()
	if tel != nil {
		metrics := tel.Metrics()
		reqCounter = metrics.Counter(observability.MHTTPRequests)
		reqHistogram = metrics.Histogram(observability.MHTTPRequestDuration)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// --- Extract W3C trace context
			ctx := prop.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
			sc := trace.SpanContextFromContext(ctx)

			// --- Request/Tenant IDs
			rid := ""
			if requestID != nil {
				rid = requestID(r)
			}
			if rid == "" {
				rid = uuid.NewString()
			}
			tid := ""
			if tenantID != nil {
				tid = tenantID(r)
			}
			w.Header().Set("X-Request-ID", rid)

			// --- Build request-scoped logger (dynamic fields only)
			fields := []observability.Field{observability.F("request_id", rid)}
			if tid != "" {
				fields = append(fields, observability.F("tenant_id", tid))
			}
			if sc.IsValid() {
				fields = append(fields,
					observability.F("trace_id", sc.TraceID().String()),
					observability.F("span_id", sc.SpanID().String()),
				)
			}
			reqLogger := base.With(fields...)
			ctx = logctx.With(ctx, reqLogger)

			// --- Metrics wrap to capture final status + duration
			start := time.Now()
			lrw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(lrw, r.WithContext(ctx))

			route := routeFromContext(ctx)             // low-cardinality template you set earlier
			statusLabel := http.StatusText(lrw.status) // or strconv.Itoa(lrw.status)

			reqCounter.Add(1,
				observability.L("method", r.Method),
				observability.L("route", route),
				observability.L("status", statusLabel),
			)
			reqHistogram.Observe(time.Since(start).Seconds(),
				observability.L("method", r.Method),
				observability.L("route", route),
				observability.L("status", statusLabel),
			)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
