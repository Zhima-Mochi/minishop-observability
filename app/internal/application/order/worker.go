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
	tel        observability.Observability

	log          observability.Logger
	reqCounter   observability.Counter   // usecase_requests_total{use_case,outcome}
	durHistogram observability.Histogram // usecase_duration_seconds{use_case}
	extCounter   observability.Counter   // external_requests_total{peer,endpoint,outcome}
	extHistogram observability.Histogram // external_request_duration_seconds{peer,endpoint}
}

const (
	workerService       = "order-worker"
	endpointInvReserved = "order.inventory_reserved"
	endpointInvFailed   = "order.inventory_reservation_failed"
)

func New(
	repo domorder.Repository,
	subscriber domoutbox.Subscriber,
	publisher domoutbox.Publisher,
	tel observability.Observability,
	logger observability.Logger,
) *Worker {
	base := logger
	if base == nil && tel != nil {
		base = tel.Logger()
	}
	if base == nil {
		base = observability.NopLogger()
	}
	base = base.With(
		observability.F("service", workerService),
	)
	metricsProvider := observability.NopMetrics()
	if tel != nil {
		metricsProvider = tel.Metrics()
	}

	return &Worker{
		repo:         repo,
		subscriber:   subscriber,
		publisher:    publisher,
		tel:          tel,
		log:          base,
		reqCounter:   metricsProvider.Counter(observability.MUsecaseRequests),
		durHistogram: metricsProvider.Histogram(observability.MUsecaseDuration),
		extCounter:   metricsProvider.Counter(observability.MExternalRequests),
		extHistogram: metricsProvider.Histogram(observability.MExternalRequestDuration),
	}
}

func (w *Worker) Start() {
	if w.subscriber == nil || w.repo == nil {
		return
	}
	w.subscriber.Subscribe(dominventory.InventoryReservedEvent{}.EventName(), w.handleInventoryReserved)
	w.subscriber.Subscribe(dominventory.InventoryReservationFailedEvent{}.EventName(), w.handleInventoryReservationFailed)
}

func (w *Worker) handleInventoryReserved(ctx context.Context, e domoutbox.Event) (err error) {
	const useCase = "order.worker.inventory_reserved"
	evt, ok := e.(dominventory.InventoryReservedEvent)
	if !ok {
		w.count(useCase, "ignored")
		return nil
	}

	ctx, span := w.tel.Tracer().Start(ctx, spanPrefix+"InventoryReserved",
		attribute.String("use_case", useCase),
		attribute.String("event", e.EventName()),
		attribute.String("order.id", evt.OrderID),
	)
	start := time.Now()
	outcome, status := "success", "OK"
	var publishErr error

	logger := logctx.From(ctx)
	if logger == nil {
		logger = w.log
	}
	logger = logger.With(
		observability.F("use_case", useCase),
		observability.F("event", e.EventName()),
		observability.F("order_id", evt.OrderID),
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

		if span != nil {
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, status)
			} else {
				span.SetStatus(codes.Ok, status)
			}
			if publishErr != nil {
				span.RecordError(publishErr)
			}
			span.End()
		}

		fields := []observability.Field{
			observability.F("outcome", outcome),
			observability.F("status", status),
			observability.F("latency_seconds", lat),
			observability.F("order_id", evt.OrderID),
		}
		if publishErr != nil {
			fields = append(fields, observability.F("event_publish_error", publishErr.Error()))
		}
		if err != nil {
			fields = append(fields, observability.F("error", err.Error()))
		}
		logger.Info("use_case_done", fields...)
	}()

	order, loadErr := w.repo.Get(ctx, evt.OrderID)
	if loadErr != nil {
		outcome, status = "error", "ORDER_LOAD_FAILED"
		return fmt.Errorf("worker: load order: %w", loadErr)
	}

	if transErr := order.InventoryReserved(); transErr != nil {
		outcome, status = "error", "STATE_TRANSITION_FAILED"
		return fmt.Errorf("worker: inventory reserved transition: %w", transErr)
	}

	if updateErr := w.repo.Update(ctx, order); updateErr != nil {
		outcome, status = "error", "ORDER_UPDATE_FAILED"
		return fmt.Errorf("worker: update order: %w", updateErr)
	}

	publishErr = w.publish(ctx, endpointInvReserved, domorder.NewOrderInventoryReservedEvent(order))
	if publishErr != nil {
		status = "EVENT_PUBLISH_FAILED"
	}

	return nil
}

func (w *Worker) handleInventoryReservationFailed(ctx context.Context, e domoutbox.Event) (err error) {
	const useCase = "order.worker.inventory_reservation_failed"
	evt, ok := e.(dominventory.InventoryReservationFailedEvent)
	if !ok {
		w.count(useCase, "ignored")
		return nil
	}

	ctx, span := w.tel.Tracer().Start(ctx, spanPrefix+"InventoryReservationFailed",
		attribute.String("use_case", useCase),
		attribute.String("event", e.EventName()),
		attribute.String("order.id", evt.OrderID),
		attribute.String("failure.reason", evt.Reason),
	)
	start := time.Now()
	outcome, status := "success", "OK"
	var publishErr error

	logger := logctx.From(ctx)
	if logger == nil {
		logger = w.log
	}
	logger = logger.With(
		observability.F("use_case", useCase),
		observability.F("event", e.EventName()),
		observability.F("order_id", evt.OrderID),
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

		if span != nil {
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, status)
			} else {
				span.SetStatus(codes.Ok, status)
			}
			if publishErr != nil {
				span.RecordError(publishErr)
			}
			span.End()
		}

		fields := []observability.Field{
			observability.F("outcome", outcome),
			observability.F("status", status),
			observability.F("latency_seconds", lat),
			observability.F("order_id", evt.OrderID),
		}
		if evt.Reason != "" {
			fields = append(fields, observability.F("failure_reason", evt.Reason))
		}
		if publishErr != nil {
			fields = append(fields, observability.F("event_publish_error", publishErr.Error()))
		}
		if err != nil {
			fields = append(fields, observability.F("error", err.Error()))
		}
		logger.Info("use_case_done", fields...)
	}()

	order, loadErr := w.repo.Get(ctx, evt.OrderID)
	if loadErr != nil {
		outcome, status = "error", "ORDER_LOAD_FAILED"
		return fmt.Errorf("worker: load order: %w", loadErr)
	}

	if transErr := order.InventoryReservationFailed(evt.Reason); transErr != nil {
		outcome, status = "error", "STATE_TRANSITION_FAILED"
		return fmt.Errorf("worker: inventory reservation failed transition: %w", transErr)
	}

	if updateErr := w.repo.Update(ctx, order); updateErr != nil {
		outcome, status = "error", "ORDER_UPDATE_FAILED"
		return fmt.Errorf("worker: update order: %w", updateErr)
	}

	publishErr = w.publish(ctx, endpointInvFailed, domorder.NewOrderInventoryReservationFailedEvent(order, evt.Reason))
	if publishErr != nil {
		status = "EVENT_PUBLISH_FAILED"
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

func (w *Worker) publish(ctx context.Context, endpoint string, event domoutbox.Event) error {
	if w.publisher == nil || event == nil {
		return nil
	}

	pubCtx, cancel := context.WithTimeout(ctx, publishTimeout)
	start := time.Now()
	err := w.publisher.Publish(pubCtx, event)
	outcome := "success"
	if err != nil {
		outcome = "error"
	} else if pubCtx.Err() != nil {
		outcome = "canceled"
		err = pubCtx.Err()
	}
	cancel()

	if w.extCounter != nil {
		w.extCounter.Add(1,
			observability.L("peer", publishPeer),
			observability.L("endpoint", endpoint),
			observability.L("outcome", outcome),
		)
	}
	if w.extHistogram != nil {
		w.extHistogram.Observe(time.Since(start).Seconds(),
			observability.L("peer", publishPeer),
			observability.L("endpoint", endpoint),
		)
	}

	return err
}
