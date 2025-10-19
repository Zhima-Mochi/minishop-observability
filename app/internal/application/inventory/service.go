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
)

type Service struct {
	invRepo      dominv.Repository
	publisher    domoutbox.Publisher
	tel          observability.Telemetry
	log          observability.Logger
	reqCounter   observability.Counter
	durHistogram observability.Histogram
}

func NewService(invRepo dominv.Repository, publisher domoutbox.Publisher, tel observability.Telemetry) *Service {
	baseLog := observability.NopLogger()
	var req observability.Counter
	var dur observability.Histogram
	if tel != nil {
		baseLog = tel.Logger().With(
			observability.F("service", inventoryService),
		)
		req = tel.Counter("usecase_requests_total")
		dur = tel.Histogram("usecase_duration_seconds")
	}

	return &Service{
		invRepo:      invRepo,
		publisher:    publisher,
		tel:          tel,
		log:          baseLog,
		reqCounter:   req,
		durHistogram: dur,
	}
}

// OnOrderCreated reacts to OrderCreated events and emits reservation result events.
func (s *Service) OnOrderCreated(ctx context.Context, e domorder.OrderCreatedEvent) (err error) {
	logger := logctx.FromOr(ctx, s.log).With(
		observability.F("use_case", useCaseInventoryReservation),
		observability.F("order_id", e.OrderID),
		observability.F("product_id", e.ProductID),
		observability.F("quantity", e.Quantity),
	)

	tracer := observability.NopTracer()
	if s.tel != nil {
		tracer = s.tel.Tracer()
	}
	ctx, span := tracer.Start(ctx, spanPrefix+inventorySpanName,
		attribute.String("use_case", useCaseInventoryReservation),
		attribute.String("order.id", e.OrderID),
		attribute.String("product.id", e.ProductID),
		attribute.Int("order.quantity", e.Quantity),
	)
	start := time.Now()
	outcome, statusText := "success", "OK"

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
		if s.reqCounter != nil {
			s.reqCounter.Add(1,
				observability.L("use_case", useCaseInventoryReservation),
				observability.L("outcome", outcome),
			)
		}
		if s.durHistogram != nil {
			s.durHistogram.Observe(latency,
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
		if err != nil {
			fields = append(fields, observability.F("error", err.Error()))
		}
		logger.Info("use_case_done", fields...)
	}()

	if err = s.invRepo.Reserve(ctx, e.ProductID, e.Quantity); err != nil {
		outcome, statusText = "error", "RESERVE_FAILED"
		reason := failureReasonFromError(err)
		logger.Warn("inventory_reserve_failed",
			observability.F("reason", reason),
			observability.F("error", err.Error()),
		)
		if publishErr := s.publishFailure(ctx, e, reason); publishErr != nil {
			logger.Error("inventory_failure_event_publish_failed",
				observability.F("reason", reason),
				observability.F("error", publishErr.Error()),
			)
		}
		return fmt.Errorf("inventory: reserve: %w", err)
	}

	if span != nil {
		span.AddEvent("inventory.reserved",
			trace.WithAttributes(
				attribute.String("order.id", e.OrderID),
				attribute.String("product.id", e.ProductID),
			),
		)
	}

	if err = s.publishReserved(ctx, e); err != nil {
		outcome, statusText = "error", "EVENT_PUBLISH_FAILED"
		logger.Warn("inventory_reserved_event_publish_failed",
			observability.F("error", err.Error()),
		)
		return fmt.Errorf("inventory: publish reserved: %w", err)
	}

	return nil
}

func (s *Service) publishReserved(ctx context.Context, e domorder.OrderCreatedEvent) error {
	if s.publisher == nil {
		return nil
	}
	return s.publisher.Publish(ctx, dominv.NewInventoryReservedEvent(e.OrderID, e.ProductID, e.Quantity))
}

func (s *Service) publishFailure(ctx context.Context, e domorder.OrderCreatedEvent, reason string) error {
	if s.publisher == nil {
		return nil
	}
	return s.publisher.Publish(ctx, dominv.NewInventoryReservationFailedEvent(e.OrderID, e.ProductID, e.Quantity, reason))
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
