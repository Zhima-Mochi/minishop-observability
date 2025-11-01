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
	publishPeer        = "outbox"
	publishEndpoint    = "order.created"
	publishTimeout     = 300 * time.Millisecond
)

var (
	ErrConflict   = domain.ErrConflict
	ErrNotFound   = domain.ErrNotFound
	ErrRepository = errors.New("order: repository failure")
)

// CreateOrderUseCase encapsulates the order creation workflow with observability hooks.
type CreateOrderUseCase struct {
	repo        domain.Repository
	idGenerator IDGenerator
	publisher   domoutbox.Publisher
	tel         observability.Observability

	// Base logger with fixed fields prebound (vendor must remain hidden).
	log observability.Logger
	// RED metrics (supplied via DI; do not instantiate inside methods).
	reqCounter   observability.Counter   // usecase_requests_total{use_case,outcome}
	durHistogram observability.Histogram // usecase_duration_seconds{use_case}

	extCounter   observability.Counter   // external_requests_total{peer,endpoint,outcome}
	extHistogram observability.Histogram // external_request_duration_seconds{peer,endpoint}
}

// NewCreateOrderUseCase wires the dependencies required to execute the use case.
func NewCreateOrderUseCase(
	repo domain.Repository,
	idGen IDGenerator,
	publisher domoutbox.Publisher,
	tel observability.Observability,
) *CreateOrderUseCase {
	baseLog := observability.NopLogger()
	if tel != nil {
		baseLog = tel.Logger()
	}
	baseLog = baseLog.With(
		observability.F("service", orderService),
	)

	metricsProvider := observability.NopMetrics()
	if tel != nil {
		metricsProvider = tel.Metrics()
	}

	req := metricsProvider.Counter(observability.MUsecaseRequests)
	dur := metricsProvider.Histogram(observability.MUsecaseDuration)
	extReq := metricsProvider.Counter(observability.MExternalRequests)
	extDur := metricsProvider.Histogram(observability.MExternalRequestDuration)

	return &CreateOrderUseCase{
		repo:         repo,
		idGenerator:  idGen,
		publisher:    publisher,
		tel:          tel,
		log:          baseLog,
		reqCounter:   req,
		durHistogram: dur,
		extCounter:   extReq,
		extHistogram: extDur,
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

// Execute performs the order creation flow.
func (uc *CreateOrderUseCase) Execute(ctx context.Context, cmd CreateOrderInput) (_ *CreateOrderResult, err error) {
	logger := logctx.FromOr(ctx, uc.log).With(observability.F("use_case", useCaseOrderCreate))

	var orderID string
	var publishErr error

	ctx, span := uc.tel.Tracer().Start(ctx, spanPrefix+"CreateOrder",
		attribute.String("use_case", useCaseOrderCreate),
		attribute.String("order.customer_id", cmd.CustomerID),
		attribute.String("order.product_id", cmd.ProductID),
	)
	start := time.Now()
	outcome, statusText := "success", "OK"

	defer func() {
		lat := time.Since(start).Seconds()

		if span != nil {
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, statusText)
			} else {
				span.SetStatus(codes.Ok, statusText)
			}
			span.End()
		}

		if uc.reqCounter != nil {
			uc.reqCounter.Add(1,
				observability.L("use_case", useCaseOrderCreate),
				observability.L("outcome", outcome),
			)
		}
		if uc.durHistogram != nil {
			uc.durHistogram.Observe(lat,
				observability.L("use_case", useCaseOrderCreate),
			)
		}

		fields := []observability.Field{
			observability.F("outcome", outcome),
			observability.F("status", statusText),
			observability.F("latency_seconds", lat),
		}
		if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
			fields = append(fields,
				observability.F("trace_id", sc.TraceID().String()),
				observability.F("span_id", sc.SpanID().String()),
			)
		}
		if publishErr != nil {
			fields = append(fields, observability.F("event_publish_error", publishErr.Error()))
		}
		if err != nil {
			fields = append(fields, observability.F("error", err.Error()))
		}

		logger.Info("use_case_done", fields...)
	}()

	if cmd.CustomerID == "" {
		outcome, statusText = "error", "CUSTOMER_ID_REQUIRED"
		return nil, newValidation("customer id is required")
	}
	if cmd.ProductID == "" {
		outcome, statusText = "error", "PRODUCT_ID_REQUIRED"
		return nil, newValidation("product id is required")
	}
	if cmd.Quantity <= 0 {
		outcome, statusText = "error", "QUANTITY_INVALID"
		return nil, newValidation("quantity must be greater than zero")
	}
	if cmd.Amount <= 0 {
		outcome, statusText = "error", "AMOUNT_INVALID"
		return nil, newValidation("amount must be greater than zero")
	}
	if err := ctx.Err(); err != nil {
		outcome, statusText = "error", "CONTEXT_CANCELED"
		return nil, err
	}

	if cmd.IdempotencyKey != "" {
		existing, repoErr := uc.repo.FindByIdempotency(ctx, cmd.CustomerID, cmd.IdempotencyKey)
		switch {
		case repoErr == nil:
			orderID = existing.ID
			statusText = "IDEMPOTENT_REPLAY"
			span.SetAttributes(attribute.String("order.status", string(existing.Status)))
			span.AddEvent("order.idempotent_replay",
				trace.WithAttributes(attribute.String("order.id", orderID)),
			)
			return &CreateOrderResult{OrderID: existing.ID, Status: existing.Status}, nil
		case errors.Is(repoErr, domain.ErrNotFound):
			// continue
		default:
			outcome, statusText = "error", "IDEMPOTENCY_LOOKUP_FAILED"
			return nil, wrapRepositoryError(repoErr)
		}
	}

	orderID = uc.idGenerator.NewID()
	entity, derr := domain.New(orderID, cmd.CustomerID, cmd.ProductID, cmd.IdempotencyKey, cmd.Quantity, cmd.Amount)
	if derr != nil {
		outcome, statusText = "error", "DOMAIN_CONSTRUCTION_FAILED"
		return nil, fmt.Errorf("order: construct: %w", derr)
	}
	if err := ctx.Err(); err != nil {
		outcome, statusText = "error", "CONTEXT_CANCELED"
		return nil, err
	}
	if err := uc.repo.Insert(ctx, entity); err != nil {
		if errors.Is(err, domain.ErrConflict) && cmd.IdempotencyKey != "" {
			if existing, lookupErr := uc.repo.FindByIdempotency(ctx, cmd.CustomerID, cmd.IdempotencyKey); lookupErr == nil {
				orderID = existing.ID
				statusText = "IDEMPOTENT_REPLAY"
				span.SetAttributes(attribute.String("order.status", string(existing.Status)))
				span.AddEvent("order.idempotent_replay",
					trace.WithAttributes(attribute.String("order.id", orderID)),
				)
				return &CreateOrderResult{OrderID: existing.ID, Status: existing.Status}, nil
			}
		}
		outcome, statusText = "error", "REPO_INSERT_FAILED"
		return nil, wrapRepositoryError(err)
	}

	if uc.publisher != nil {
		pubCtx, cancel := context.WithTimeout(ctx, publishTimeout)
		pubStart := time.Now()
		pubOutcome := "success"

		publishErr = uc.publisher.Publish(pubCtx, domain.NewOrderCreatedEvent(entity))
		if publishErr != nil {
			pubOutcome = "error"
			statusText = "EVENT_PUBLISH_FAILED"
		} else if pubCtx.Err() != nil {
			pubOutcome = "canceled"
			publishErr = pubCtx.Err()
			statusText = "EVENT_PUBLISH_TIMEOUT"
		}
		cancel()

		if uc.extCounter != nil {
			uc.extCounter.Add(1,
				observability.L("peer", publishPeer),
				observability.L("endpoint", publishEndpoint),
				observability.L("outcome", pubOutcome),
			)
		}
		if uc.extHistogram != nil {
			uc.extHistogram.Observe(time.Since(pubStart).Seconds(),
				observability.L("peer", publishPeer),
				observability.L("endpoint", publishEndpoint),
			)
		}
	}

	span.SetAttributes(attribute.String("order.status", string(entity.Status)))
	span.AddEvent("order.created",
		trace.WithAttributes(
			attribute.String("order.id", orderID),
		),
	)

	return &CreateOrderResult{OrderID: entity.ID, Status: entity.Status}, nil
}

// CreateOrder preserves backwards compatibility with existing callers that have not been migrated yet.
func (uc *CreateOrderUseCase) CreateOrder(ctx context.Context, input CreateOrderInput) (*CreateOrderResult, error) {
	return uc.Execute(ctx, input)
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
