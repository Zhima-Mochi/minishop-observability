package order

import (
	"context"
	"errors"
	"fmt"
	"time"

	domain "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/order"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/infrastructure/outbox"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/pkg/logging"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	prometheus "github.com/prometheus/client_golang/prometheus"
)

const (
	componentOrderService = "order_service"
	useCaseOrderCreate    = "order.create"
	tracerName            = "minishop.order"
	spanPrefix            = "UC."
)

var (
	ErrConflict   = domain.ErrConflict
	ErrNotFound   = domain.ErrNotFound
	ErrRepository = errors.New("order: repository failure")
)

type Service struct {
	repo        domain.Repository
	idGenerator IDGenerator
	publisher   outbox.Publisher
}

func NewService(
	repo domain.Repository,
	idGen IDGenerator,
	publisher outbox.Publisher,
) *Service {
	return &Service{
		repo:        repo,
		idGenerator: idGen,
		publisher:   publisher,
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
	ctx, span := s.startSpan(ctx)
	defer span.End()

	logger := logging.FromContext(ctx).With(zap.String("use_case", useCaseOrderCreate))

	start := time.Now()
	statusText := "OK"

	defer func() {
		duration := time.Since(start)
		reqs := prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"use_case", "status"})
		dur := prometheus.NewHistogramVec(prometheus.HistogramOpts{}, []string{"use_case", "status"})

		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, statusText)
		} else {
			span.SetStatus(codes.Ok, statusText)
		}

		reqs.WithLabelValues(useCaseOrderCreate, statusText).Inc()
		dur.WithLabelValues(useCaseOrderCreate, statusText).Observe(duration.Seconds())

		logger.Info("use_case_done", zap.String("use_case", useCaseOrderCreate), zap.String("status", statusText))
	}()

	if input.CustomerID == "" {
		statusText = "CUSTOMER_ID_REQUIRED"
		return nil, newValidation("customer id is required")
	}
	if input.ProductID == "" {
		statusText = "PRODUCT_ID_REQUIRED"
		return nil, newValidation("product id is required")
	}
	if input.Quantity <= 0 {
		statusText = "QUANTITY_INVALID"
		return nil, newValidation("quantity must be greater than zero")
	}
	if input.Amount <= 0 {
		statusText = "AMOUNT_INVALID"
		return nil, newValidation("amount must be greater than zero")
	}

	if err := ctx.Err(); err != nil {
		statusText = "CONTEXT_CANCELED"
		return nil, err
	}

	if input.IdempotencyKey != "" {
		existing, repoErr := s.repo.FindByIdempotency(ctx, input.CustomerID, input.IdempotencyKey)
		switch {
		case repoErr == nil:
			statusText = "IDEMPOTENT_REPLAY"
			span.SetAttributes(attribute.String("order.status", string(existing.Status)))
			span.AddEvent("order.idempotent_replay",
				trace.WithAttributes(attribute.String("order.id", existing.ID)),
			)
			return &CreateOrderResult{
				OrderID: existing.ID,
				Status:  existing.Status,
			}, nil
		case errors.Is(repoErr, domain.ErrNotFound):
			// proceed to create a new order
		default:
			statusText = "IDEMPOTENCY_LOOKUP_FAILED"
			return nil, wrapRepositoryError(repoErr)
		}
	}

	generatedID := s.idGenerator.NewID()
	entity, domainErr := domain.New(generatedID, input.CustomerID, input.ProductID, input.IdempotencyKey, input.Quantity, input.Amount)
	if domainErr != nil {
		statusText = "DOMAIN_CONSTRUCTION_FAILED"
		return nil, fmt.Errorf("order: construct: %w", domainErr)
	}

	if err := ctx.Err(); err != nil {
		statusText = "CONTEXT_CANCELED"
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
				return &CreateOrderResult{
					OrderID: existing.ID,
					Status:  existing.Status,
				}, nil
			}
		}
		statusText = "REPO_INSERT_FAILED"
		return nil, wrapRepositoryError(err)
	}

	evt := domain.NewOrderCreatedEvent(entity)
	if err := s.publisher.Publish(ctx, evt); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "EVENT_PUBLISH_FAILED")
		statusText = "EVENT_PUBLISH_FAILED"
		logger.Warn("event_publish_failed",
			zap.String("event", evt.EventName()),
			zap.String("order_id", entity.ID),
			zap.Error(err),
		)
	}

	span.SetAttributes(attribute.String("order.status", string(entity.Status)))
	span.AddEvent("order.created",
		trace.WithAttributes(attribute.String("order.id", entity.ID)),
	)

	return &CreateOrderResult{
		OrderID: entity.ID,
		Status:  entity.Status,
	}, nil
}

func (s *Service) Get(ctx context.Context, id string) (*domain.Order, error) {
	if id == "" {
		return nil, newValidation("order id is required")
	}
	return s.repo.Get(ctx, id)
}

func (s *Service) startSpan(ctx context.Context) (context.Context, trace.Span) {
	return otel.Tracer(tracerName).Start(ctx, spanPrefix+"CreateOrder",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("use_case", useCaseOrderCreate),
		),
	)
}

type validationError struct {
	msg string
}

func (e validationError) Error() string {
	return e.msg
}

func newValidation(msg string) error {
	return validationError{msg: fmt.Sprintf("order: %s", msg)}
}

// IsValidation reports whether the provided error is a validation error emitted by the order use case.
func IsValidation(err error) bool {
	var v validationError
	return errors.As(err, &v)
}

func wrapRepositoryError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, domain.ErrNotFound) {
		return ErrNotFound
	}
	if errors.Is(err, domain.ErrConflict) {
		return ErrConflict
	}
	return fmt.Errorf("%w: %v", ErrRepository, err)
}
