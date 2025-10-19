package order

import (
	"context"
	"errors"
	"fmt"
	"time"

	domain "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/order"
	domoutbox "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/outbox"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability/logctx"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	orderService       = "order-service"
	useCaseOrderCreate = "order.create"
	spanPrefix         = "UC."
)

var (
	ErrConflict   = domain.ErrConflict
	ErrNotFound   = domain.ErrNotFound
	ErrRepository = errors.New("order: repository failure")
)

type Service struct {
	repo        domain.Repository
	idGenerator IDGenerator
	publisher   domoutbox.Publisher
	tel         observability.Telemetry

	// Base logger with fixed fields prebound (vendor must remain hidden).
	log observability.Logger
	// RED metrics (supplied via DI; do not instantiate inside methods).
	reqCounter   observability.Counter   // usecase_requests_total{use_case,outcome}
	durHistogram observability.Histogram // usecase_duration_seconds{use_case}
}

func NewService(
	repo domain.Repository,
	idGen IDGenerator,
	publisher domoutbox.Publisher,
	tel observability.Telemetry,
) *Service {
	baseLog := tel.Logger().With(
		observability.F("service", orderService),
	)

	req := tel.Counter("usecase_requests_total")
	dur := tel.Histogram("usecase_duration_seconds")

	return &Service{
		repo:         repo,
		idGenerator:  idGen,
		publisher:    publisher,
		tel:          tel,
		log:          baseLog,
		reqCounter:   req,
		durHistogram: dur,
	}
}

type CreateOrderInput struct {
	IdempotencyKey string
	CustomerID     string
	ProductID      string
	Quantity       int
	Amount         int64
}
type CreateOrderResult struct {
	OrderID string
	Status  domain.Status
}

func (s *Service) CreateOrder(ctx context.Context, input CreateOrderInput) (_ *CreateOrderResult, err error) {
	logger := logctx.FromOr(ctx, s.log).With(observability.F("use_case", useCaseOrderCreate))

	// sub span (server span in HTTP middleware)
	ctx, span := s.tel.Tracer().Start(ctx, spanPrefix+"CreateOrder",
		attribute.String("use_case", useCaseOrderCreate),
	)
	start := time.Now()
	outcome, statusText := "success", "OK"
	var orderID string

	defer func() {
		lat := time.Since(start).Seconds()

		// RED
		if s.reqCounter != nil {
			s.reqCounter.Add(1, observability.L("use_case", useCaseOrderCreate), observability.L("outcome", outcome))
		}
		if s.durHistogram != nil {
			s.durHistogram.Observe(lat, observability.L("use_case", useCaseOrderCreate))
		}

		// One semantic log
		fields := []observability.Field{
			observability.F("outcome", outcome),
			observability.F("status", statusText),
			observability.F("latency_seconds", lat),
		}
		if orderID != "" {
			fields = append(fields, observability.F("order_id", orderID))
		}
		if err != nil {
			fields = append(fields, observability.F("error", err.Error()))
			span.RecordError(err)
			span.SetStatus(codes.Error, statusText)
		} else {
			span.SetStatus(codes.Ok, statusText)
		}
		logger.Info("use_case_done", fields...)

		span.End()
	}()

	// validation
	if input.CustomerID == "" {
		outcome, statusText = "error", "CUSTOMER_ID_REQUIRED"
		return nil, newValidation("customer id is required")
	}
	if input.ProductID == "" {
		outcome, statusText = "error", "PRODUCT_ID_REQUIRED"
		return nil, newValidation("product id is required")
	}
	if input.Quantity <= 0 {
		outcome, statusText = "error", "QUANTITY_INVALID"
		return nil, newValidation("quantity must be greater than zero")
	}
	if input.Amount <= 0 {
		outcome, statusText = "error", "AMOUNT_INVALID"
		return nil, newValidation("amount must be greater than zero")
	}
	if err := ctx.Err(); err != nil {
		outcome, statusText = "error", "CONTEXT_CANCELED"
		return nil, err
	}

	// idempotency
	if input.IdempotencyKey != "" {
		existing, repoErr := s.repo.FindByIdempotency(ctx, input.CustomerID, input.IdempotencyKey)
		switch {
		case repoErr == nil:
			statusText = "IDEMPOTENT_REPLAY"
			span.SetAttributes(attribute.String("order.status", string(existing.Status)))
			span.AddEvent("order.idempotent_replay",
				trace.WithAttributes(attribute.String("order.id", existing.ID)),
			)
			return &CreateOrderResult{OrderID: existing.ID, Status: existing.Status}, nil
		case errors.Is(repoErr, domain.ErrNotFound):
			// go on
		default:
			outcome, statusText = "error", "IDEMPOTENCY_LOOKUP_FAILED"
			return nil, wrapRepositoryError(repoErr)
		}
	}

	// construction
	orderID = s.idGenerator.NewID()
	entity, derr := domain.New(orderID, input.CustomerID, input.ProductID, input.IdempotencyKey, input.Quantity, input.Amount)
	if derr != nil {
		outcome, statusText = "error", "DOMAIN_CONSTRUCTION_FAILED"
		return nil, fmt.Errorf("order: construct: %w", derr)
	}
	if err := ctx.Err(); err != nil {
		outcome, statusText = "error", "CONTEXT_CANCELED"
		return nil, err
	}
	if err := s.repo.Insert(ctx, entity); err != nil {
		if errors.Is(err, domain.ErrConflict) && input.IdempotencyKey != "" {
			if existing, lookupErr := s.repo.FindByIdempotency(ctx, input.CustomerID, input.IdempotencyKey); lookupErr == nil {
				statusText = "IDEMPOTENT_REPLAY"
				span.SetAttributes(attribute.String("order.status", string(existing.Status)))
				span.AddEvent("order.idempotent_replay",
					trace.WithAttributes(attribute.String("order.id", existing.ID)),
				)
				return &CreateOrderResult{OrderID: existing.ID, Status: existing.Status}, nil
			}
		}
		outcome, statusText = "error", "REPO_INSERT_FAILED"
		return nil, wrapRepositoryError(err)
	}

	// publish event (best-effort; don't block tail)
	if s.publisher != nil {
		if pubErr := s.publisher.Publish(ctx, domain.NewOrderCreatedEvent(entity)); pubErr != nil {
			outcome, statusText = "success", "EVENT_PUBLISH_FAILED"
			logger.Warn("event_publish_failed",
				observability.F("event", "order.created"),
				observability.F("order_id", entity.ID),
				observability.F("error", pubErr.Error()),
			)
		}
	}

	span.SetAttributes(attribute.String("order.status", string(entity.Status)))
	span.AddEvent("order.created", trace.WithAttributes(attribute.String("order.id", entity.ID)))

	return &CreateOrderResult{OrderID: entity.ID, Status: entity.Status}, nil
}

// Get: keep it simple
func (s *Service) Get(ctx context.Context, id string) (*domain.Order, error) {
	if id == "" {
		return nil, newValidation("order id is required")
	}
	return s.repo.Get(ctx, id)
}

func wrapRepositoryError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, domain.ErrConflict):
		return ErrConflict
	default:
		return fmt.Errorf("%w: %w", ErrRepository, err)
	}
}

func newValidation(msg string) error {
	return fmt.Errorf("validation: %w", errors.New(msg))
}
