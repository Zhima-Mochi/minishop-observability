package inventory

import (
	"context"
	"errors"
	"fmt"
	"time"

	dominv "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/inventory"
	domorder "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/order"
	domoutbox "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/outbox"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability/logctx"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	inventoryService            = "inventory-service"
	useCaseInventoryReservation = "inventory.reserve"
	inventorySpanName           = "OnOrderCreated"
	spanPrefix                  = "UC."
	publishPeer                 = "outbox"
	endpointReserved            = "inventory.reserved"
	endpointReservationFailed   = "inventory.reservation_failed"
	publishTimeout              = 300 * time.Millisecond
)

// ReservationResult exposes the outcome of the inventory reservation attempt.
type ReservationResult struct {
	Reserved      bool
	FailureReason string
}

type ReserveInventoryUseCase struct {
	invRepo      dominv.Repository
	publisher    domoutbox.Publisher
	log          observability.Logger
	tracer       observability.Tracer
	reqCounter   observability.Counter
	durHistogram observability.Histogram
	extCounter   observability.Counter
	extHistogram observability.Histogram
}

func NewReserveInventoryUseCase(invRepo dominv.Repository, publisher domoutbox.Publisher, tel observability.Observability) *ReserveInventoryUseCase {
	baseLog := observability.NopLogger().With(
		observability.F("service", inventoryService),
	)
	tracer := observability.NopTracer()
	metricsProvider := observability.NopMetrics()
	if tel != nil {
		baseLog = tel.Logger().With(
			observability.F("service", inventoryService),
		)
		tracer = tel.Tracer()
		metricsProvider = tel.Metrics()
	}
	req := metricsProvider.Counter(observability.MUsecaseRequests)
	dur := metricsProvider.Histogram(observability.MUsecaseDuration)
	extReq := metricsProvider.Counter(observability.MExternalRequests)
	extDur := metricsProvider.Histogram(observability.MExternalRequestDuration)

	return &ReserveInventoryUseCase{
		invRepo:      invRepo,
		publisher:    publisher,
		log:          baseLog,
		tracer:       tracer,
		reqCounter:   req,
		durHistogram: dur,
		extCounter:   extReq,
		extHistogram: extDur,
	}
}

// Execute reacts to OrderCreated events and emits reservation result events.
func (uc *ReserveInventoryUseCase) Execute(ctx context.Context, e domorder.OrderCreatedEvent) (_ *ReservationResult, err error) {
	logger := logctx.FromOr(ctx, uc.log).With(
		observability.F("use_case", useCaseInventoryReservation),
		observability.F("order_id", e.OrderID),
		observability.F("product_id", e.ProductID),
		observability.F("quantity", e.Quantity),
	)

	ctx, span := uc.tracer.Start(ctx, spanPrefix+inventorySpanName,
		attribute.String("use_case", useCaseInventoryReservation),
		attribute.String("order.id", e.OrderID),
		attribute.String("product.id", e.ProductID),
		attribute.Int("order.quantity", e.Quantity),
	)
	start := time.Now()
	outcome, statusText := "success", "OK"
	var failureReason string
	var publishReservedErr error
	var publishFailureErr error
	result := &ReservationResult{Reserved: true}

	defer func() {
		if span != nil {
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, statusText)
			} else {
				span.SetStatus(codes.Ok, statusText)
			}
			span.End()
		}

		latency := time.Since(start).Seconds()
		if uc.reqCounter != nil {
			uc.reqCounter.Add(1,
				observability.L("use_case", useCaseInventoryReservation),
				observability.L("outcome", outcome),
			)
		}
		if uc.durHistogram != nil {
			uc.durHistogram.Observe(latency,
				observability.L("use_case", useCaseInventoryReservation),
			)
		}

		fields := []observability.Field{
			observability.F("outcome", outcome),
			observability.F("status", statusText),
			observability.F("latency_seconds", latency),
			observability.F("order_id", e.OrderID),
			observability.F("product_id", e.ProductID),
			observability.F("quantity", e.Quantity),
		}
		if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
			fields = append(fields,
				observability.F("trace_id", sc.TraceID().String()),
				observability.F("span_id", sc.SpanID().String()),
			)
		}
		if failureReason != "" {
			fields = append(fields, observability.F("failure_reason", failureReason))
		}
		if publishReservedErr != nil {
			fields = append(fields, observability.F("reservation_event_error", publishReservedErr.Error()))
		}
		if publishFailureErr != nil {
			fields = append(fields, observability.F("failure_event_error", publishFailureErr.Error()))
		}
		if err != nil {
			fields = append(fields, observability.F("error", err.Error()))
		}

		logger.Info("use_case_done", fields...)
	}()

	if err = uc.invRepo.Reserve(ctx, e.ProductID, e.Quantity); err != nil {
		outcome, statusText = "error", "RESERVE_FAILED"
		failureReason = failureReasonFromError(err)
		result.Reserved = false
		result.FailureReason = failureReason
		publishFailureErr = uc.publish(ctx, endpointReservationFailed, dominv.NewInventoryReservationFailedEvent(e.OrderID, e.ProductID, e.Quantity, failureReason))
		return result, fmt.Errorf("inventory: reserve: %w", err)
	}

	if span != nil {
		span.AddEvent("inventory.reserved",
			trace.WithAttributes(
				attribute.String("order.id", e.OrderID),
				attribute.String("product.id", e.ProductID),
			),
		)
	}

	publishReservedErr = uc.publish(ctx, endpointReserved, dominv.NewInventoryReservedEvent(e.OrderID, e.ProductID, e.Quantity))
	if publishReservedErr != nil {
		outcome, statusText = "error", "EVENT_PUBLISH_FAILED"
		return result, fmt.Errorf("inventory: publish reserved: %w", publishReservedErr)
	}

	return result, nil
}

// OnOrderCreated keeps the old API available while the rest of the codebase migrates to Execute.
func (uc *ReserveInventoryUseCase) OnOrderCreated(ctx context.Context, e domorder.OrderCreatedEvent) error {
	_, err := uc.Execute(ctx, e)
	return err
}

func (uc *ReserveInventoryUseCase) publish(ctx context.Context, endpoint string, event domoutbox.Event) error {
	if uc.publisher == nil || event == nil {
		return nil
	}

	pubCtx, cancel := context.WithTimeout(ctx, publishTimeout)
	start := time.Now()
	err := uc.publisher.Publish(pubCtx, event)
	outcome := "success"
	if err != nil {
		outcome = "error"
	} else if pubCtx.Err() != nil {
		outcome = "canceled"
		err = pubCtx.Err()
	}
	cancel()

	if uc.extCounter != nil {
		uc.extCounter.Add(1,
			observability.L("peer", publishPeer),
			observability.L("endpoint", endpoint),
			observability.L("outcome", outcome),
		)
	}
	if uc.extHistogram != nil {
		uc.extHistogram.Observe(time.Since(start).Seconds(),
			observability.L("peer", publishPeer),
			observability.L("endpoint", endpoint),
		)
	}

	return err
}

func failureReasonFromError(err error) string {
	switch {
	case errors.Is(err, dominv.ErrNotFound):
		return dominv.FailureReasonNotFound
	case errors.Is(err, dominv.ErrInvalidQuantity):
		return dominv.FailureReasonPersistenceError
	case errors.Is(err, dominv.ErrInsufficientStock):
		return dominv.FailureReasonInsufficientStock
	default:
		return err.Error()
	}
}
