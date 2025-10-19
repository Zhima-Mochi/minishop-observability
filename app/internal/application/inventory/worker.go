package inventory

import (
	"context"
	"fmt"
	"time"

	domorder "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/order"
	domoutbox "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/outbox"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability/logctx"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const workerService = "inventory_worker"

type Worker struct {
	subscriber domoutbox.Subscriber
	service    *Service
	tel        observability.Telemetry

	log          observability.Logger
	reqCounter   observability.Counter   // usecase_requests_total{use_case,outcome}
	durHistogram observability.Histogram // usecase_duration_seconds{use_case}
}

func New(
	subscriber domoutbox.Subscriber,
	service *Service,
	tel observability.Telemetry,
	logger observability.Logger,
) *Worker {
	baseLogger := logger
	if baseLogger == nil {
		baseLogger = tel.Logger()
	}
	return &Worker{
		subscriber:   subscriber,
		service:      service,
		tel:          tel,
		log:          baseLogger.With(observability.F("service", workerService)),
		reqCounter:   tel.Counter("usecase_requests_total"),
		durHistogram: tel.Histogram("usecase_duration_seconds"),
	}
}

func (w *Worker) Start() {
	if w.subscriber == nil || w.service == nil {
		return
	}
	w.subscriber.Subscribe(domorder.OrderCreatedEvent{}.EventName(), w.handleOrderCreated)
}

func (w *Worker) handleOrderCreated(ctx context.Context, e domoutbox.Event) error {
	const useCase = "inventory.worker.order_created"
	evt, ok := e.(domorder.OrderCreatedEvent)
	if !ok {
		w.count(useCase, "ignored")
		return nil
	}

	ctx, span := w.tel.Tracer().Start(ctx, spanPrefix+"OrderCreated",
		attribute.String("use_case", useCase),
		attribute.String("event", e.EventName()),
	)
	start := time.Now()
	outcome, status := "success", "OK"

	logger := logctx.From(ctx)
	if logger == nil {
		logger = w.log
	}
	logger = logger.With(
		observability.F("use_case", useCase),
		observability.F("event", e.EventName()),
	)
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		logger = logger.With(
			observability.F("trace_id", sc.TraceID().String()),
			observability.F("span_id", sc.SpanID().String()),
		)
	}

	ctx = logctx.With(ctx, logger)

	defer func() {
		lat := time.Since(start).Seconds()
		w.observe(useCase, outcome, lat)

		fields := []observability.Field{
			observability.F("outcome", outcome),
			observability.F("status", status),
			observability.F("latency_seconds", lat),
		}
		fields = append(fields, observability.F("order_id", evt.OrderID))

		logger.Info("use_case_done", fields...)

		if outcome == "error" {
			span.SetStatus(codes.Error, status)
		} else {
			span.SetStatus(codes.Ok, status)
		}
		span.End()
	}()

	if err := w.service.OnOrderCreated(ctx, evt); err != nil {
		outcome, status = "error", "STATE_TRANSITION_FAILED"
		return fmt.Errorf("worker: inventory reservation transition: %w", err)
	}

	return nil
}

func (w *Worker) count(useCase, outcome string) {
	if w.reqCounter != nil {
		w.reqCounter.Add(1,
			observability.L("use_case", useCase),
			observability.L("outcome", outcome),
		)
	}
}

func (w *Worker) observe(useCase string, outcome string, latencySeconds float64) {
	w.count(useCase, outcome)
	if w.durHistogram != nil {
		w.durHistogram.Observe(latencySeconds,
			observability.L("use_case", useCase),
		)
	}
}
