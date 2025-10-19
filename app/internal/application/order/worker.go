package order

import (
	"context"
	"fmt"
	"time"

	dominventory "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/inventory"
	domorder "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/order"
	domoutbox "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/outbox"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability/logctx"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type Worker struct {
	repo       domorder.Repository
	subscriber domoutbox.Subscriber
	publisher  domoutbox.Publisher
	tel        observability.Telemetry

	log          observability.Logger
	reqCounter   observability.Counter   // usecase_requests_total{use_case,outcome}
	durHistogram observability.Histogram // usecase_duration_seconds{use_case}
}

const (
	workerService = "order-worker"
)

func New(
	repo domorder.Repository,
	subscriber domoutbox.Subscriber,
	publisher domoutbox.Publisher,
	tel observability.Telemetry,
	logger observability.Logger,
) *Worker {
	base := logger
	if base == nil {
		base = tel.Logger()
	}
	base = base.With(
		observability.F("service", workerService),
	)

	return &Worker{
		repo:         repo,
		subscriber:   subscriber,
		publisher:    publisher,
		tel:          tel,
		log:          base,
		reqCounter:   tel.Counter("usecase_requests_total"),
		durHistogram: tel.Histogram("usecase_duration_seconds"),
	}
}

func (w *Worker) Start() {
	if w.subscriber == nil || w.repo == nil {
		return
	}
	w.subscriber.Subscribe(dominventory.InventoryReservedEvent{}.EventName(), w.handleInventoryReserved)
	w.subscriber.Subscribe(dominventory.InventoryReservationFailedEvent{}.EventName(), w.handleInventoryReservationFailed)
}

func (w *Worker) handleInventoryReserved(ctx context.Context, e domoutbox.Event) error {
	const useCase = "order.worker.inventory_reserved"
	evt, ok := e.(dominventory.InventoryReservedEvent)
	if !ok {
		w.count(useCase, "ignored")
		return nil
	}

	ctx, span := w.tel.Tracer().Start(ctx, spanPrefix+"InventoryReserved",
		attribute.String("use_case", useCase),
		attribute.String("event", e.EventName()),
	)
	start := time.Now()
	outcome, status := "success", "OK"
	var orderID string

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
	// pass logger back to ctx, so downstream repo/client can also fetch same logger
	ctx = logctx.With(ctx, logger)

	defer func() {
		lat := time.Since(start).Seconds()
		w.observe(useCase, outcome, lat)

		fields := []observability.Field{
			observability.F("outcome", outcome),
			observability.F("status", status),
			observability.F("latency_seconds", lat),
		}
		if orderID != "" {
			fields = append(fields, observability.F("order_id", orderID))
		}
		logger.Info("use_case_done", fields...)

		if outcome == "error" {
			span.SetStatus(codes.Error, status)
		} else {
			span.SetStatus(codes.Ok, status)
		}
		span.End()
	}()

	order, err := w.repo.Get(ctx, evt.OrderID)
	if err != nil {
		outcome, status = "error", "ORDER_LOAD_FAILED"
		return fmt.Errorf("worker: load order: %w", err)
	}
	orderID = order.ID

	if err := order.InventoryReserved(); err != nil {
		outcome, status = "error", "STATE_TRANSITION_FAILED"
		return fmt.Errorf("worker: inventory reserved transition: %w", err)
	}

	if err := w.repo.Update(ctx, order); err != nil {
		outcome, status = "error", "ORDER_UPDATE_FAILED"
		return fmt.Errorf("worker: update order: %w", err)
	}

	// publish event (best-effort; don't block tail)
	if w.publisher != nil {
		ctxPub, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		defer cancel()
		if err := w.publisher.Publish(ctxPub, domorder.NewOrderInventoryReservedEvent(order)); err != nil {
			// still success, but record in trace
			span.RecordError(err)
			status = "EVENT_PUBLISH_FAILED"
			logger.Warn("event_publish_failed",
				observability.F("event", "order.inventory_reserved"),
				observability.F("order_id", order.ID),
				observability.F("error", err.Error()),
			)
		}
	}

	span.SetAttributes(
		attribute.String("order.id", order.ID),
		attribute.String("event", e.EventName()),
	)
	return nil
}

func (w *Worker) handleInventoryReservationFailed(ctx context.Context, e domoutbox.Event) error {
	const useCase = "order.worker.inventory_reservation_failed"
	evt, ok := e.(dominventory.InventoryReservationFailedEvent)
	if !ok {
		w.count(useCase, "ignored")
		return nil
	}

	ctx, span := w.tel.Tracer().Start(ctx, spanPrefix+"InventoryReservationFailed",
		attribute.String("use_case", useCase),
		attribute.String("event", e.EventName()),
	)
	start := time.Now()
	outcome, status := "success", "OK"
	var orderID string

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
		if orderID != "" {
			fields = append(fields, observability.F("order_id", orderID))
		}
		if evt.Reason != "" {
			fields = append(fields, observability.F("reason", evt.Reason))
		}
		logger.Info("use_case_done", fields...)

		if outcome == "error" {
			span.SetStatus(codes.Error, status)
		} else {
			span.SetStatus(codes.Ok, status)
		}
		span.End()
	}()

	order, err := w.repo.Get(ctx, evt.OrderID)
	if err != nil {
		outcome, status = "error", "ORDER_LOAD_FAILED"
		return fmt.Errorf("worker: load order: %w", err)
	}
	orderID = order.ID

	if err := order.InventoryReservationFailed(evt.Reason); err != nil {
		outcome, status = "error", "STATE_TRANSITION_FAILED"
		return fmt.Errorf("worker: reservation failed transition: %w", err)
	}

	if err := w.repo.Update(ctx, order); err != nil {
		outcome, status = "error", "ORDER_UPDATE_FAILED"
		return fmt.Errorf("worker: update order: %w", err)
	}

	if w.publisher != nil {
		ctxPub, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		defer cancel()
		if err := w.publisher.Publish(ctxPub, domorder.NewOrderInventoryReservationFailedEvent(order, evt.Reason)); err != nil {
			span.RecordError(err)
			status = "EVENT_PUBLISH_FAILED"
			logger.Warn("event_publish_failed",
				observability.F("event", "order.inventory_reservation_failed"),
				observability.F("order_id", order.ID),
				observability.F("error", err.Error()),
			)
		}
	}

	span.SetAttributes(
		attribute.String("order.id", order.ID),
		attribute.String("event", e.EventName()),
		attribute.String("reason", evt.Reason),
	)
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
