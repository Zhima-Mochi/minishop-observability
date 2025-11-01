package inventory

import (
	"context"
	"fmt"
	"time"

	"github.com/Zhima-Mochi/minishop-observability/app/internal/application"
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
	useCase    application.UseCase[domorder.OrderCreatedEvent, *ReservationResult]
	tel        observability.Observability

	log          observability.Logger
	reqCounter   observability.Counter   // usecase_requests_total{use_case,outcome}
	durHistogram observability.Histogram // usecase_duration_seconds{use_case}
}

func New(
	subscriber domoutbox.Subscriber,
	useCase application.UseCase[domorder.OrderCreatedEvent, *ReservationResult],
	tel observability.Observability,
	logger observability.Logger,
) *Worker {
	baseLogger := logger
	if baseLogger == nil && tel != nil {
		baseLogger = tel.Logger()
	}
	if baseLogger == nil {
		baseLogger = observability.NopLogger()
	}
	metricsProvider := observability.NopMetrics()
	if tel != nil {
		metricsProvider = tel.Metrics()
	}
	return &Worker{
		subscriber:   subscriber,
		useCase:      useCase,
		tel:          tel,
		log:          baseLogger.With(observability.F("service", workerService)),
		reqCounter:   metricsProvider.Counter(observability.MUsecaseRequests),
		durHistogram: metricsProvider.Histogram(observability.MUsecaseDuration),
	}
}

func (w *Worker) Start() {
	if w.subscriber == nil || w.useCase == nil {
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
	var failureReason string

	logger := logctx.From(ctx)
	if logger == nil {
		logger = w.log
	}
	logger = logger.With(
		observability.F("use_case", useCase),
		observability.F("event", e.EventName()),
		observability.F("order_id", evt.OrderID),
		observability.F("product_id", evt.ProductID),
		observability.F("quantity", evt.Quantity),
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
			observability.F("order_id", evt.OrderID),
			observability.F("product_id", evt.ProductID),
			observability.F("quantity", evt.Quantity),
		}
		if failureReason != "" {
			fields = append(fields, observability.F("failure_reason", failureReason))
		}

		logger.Info("use_case_done", fields...)

		if outcome == "error" {
			span.SetStatus(codes.Error, status)
		} else {
			span.SetStatus(codes.Ok, status)
		}
		span.End()
	}()

	res, err := w.useCase.Execute(ctx, evt)
	if err != nil {
		outcome, status = "error", "STATE_TRANSITION_FAILED"
		if res != nil {
			failureReason = res.FailureReason
		}
		return fmt.Errorf("worker: inventory reservation transition: %w", err)
	}
	if res != nil && !res.Reserved && res.FailureReason != "" {
		failureReason = res.FailureReason
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
