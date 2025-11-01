package payment

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"

	domorder "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/order"
	pstat "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/payment"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability/logctx"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	paymentService          = "payment-service"
	useCasePaymentProcess   = "payment.process"
	paymentSpanName         = "ProcessPayment"
	spanPrefix              = "UC."
	defaultPaymentSuccess   = 0.7
	paymentDeclinedReason   = "payment_declined"
	paymentSimulationFailed = "PAYMENT_SIMULATION_FAILED"
)

type ProcessPaymentInput struct {
	OrderID string
	Amount  int64
}

type ProcessPaymentResult struct {
	Status pstat.Status
}

type ProcessPaymentUseCase struct {
	mu          sync.Mutex
	random      *rand.Rand
	successRate float64
	orderRepo   domorder.Repository
	tel         observability.Observability
	log         observability.Logger
	reqCounter  observability.Counter
	durHist     observability.Histogram
}

func NewProcessPaymentUseCase(orderRepo domorder.Repository, tel observability.Observability) *ProcessPaymentUseCase {
	baseLog := observability.NopLogger().With(
		observability.F("service", paymentService),
	)
	metricsProvider := observability.NopMetrics()
	if tel != nil {
		baseLog = tel.Logger().With(
			observability.F("service", paymentService),
		)
		metricsProvider = tel.Metrics()
	}
	req := metricsProvider.Counter(observability.MUsecaseRequests)
	dur := metricsProvider.Histogram(observability.MUsecaseDuration)

	return &ProcessPaymentUseCase{
		random:      rand.New(rand.NewSource(time.Now().UnixNano())),
		successRate: defaultPaymentSuccess,
		orderRepo:   orderRepo,
		tel:         tel,
		log:         baseLog,
		reqCounter:  req,
		durHist:     dur,
	}
}

// Execute checks order existence and status, then simulates payment and updates order state.
func (uc *ProcessPaymentUseCase) Execute(ctx context.Context, cmd ProcessPaymentInput) (_ *ProcessPaymentResult, err error) {
	logger := logctx.FromOr(ctx, uc.log).With(
		observability.F("use_case", useCasePaymentProcess),
		observability.F("order_id", cmd.OrderID),
		observability.F("amount", cmd.Amount),
	)

	tracer := observability.NopTracer()
	if uc.tel != nil {
		tracer = uc.tel.Tracer()
	}

	ctx, span := tracer.Start(ctx, spanPrefix+paymentSpanName,
		attribute.String("use_case", useCasePaymentProcess),
		attribute.String("order.id", cmd.OrderID),
		attribute.Int64("payment.amount_requested", cmd.Amount),
	)
	start := time.Now()
	outcome, statusText := "success", "OK"
	status := pstat.StatusFailed
	result := &ProcessPaymentResult{Status: status}
	var failureReason string

	defer func() {
		if span != nil {
			span.SetAttributes(
				attribute.String("payment.status", string(result.Status)),
			)
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
				observability.L("use_case", useCasePaymentProcess),
				observability.L("outcome", outcome),
			)
		}
		if uc.durHist != nil {
			uc.durHist.Observe(latency,
				observability.L("use_case", useCasePaymentProcess),
			)
		}

		fields := []observability.Field{
			observability.F("outcome", outcome),
			observability.F("status", statusText),
			observability.F("latency_seconds", latency),
			observability.F("order_id", cmd.OrderID),
			observability.F("amount", cmd.Amount),
			observability.F("payment_status", string(result.Status)),
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
		if err != nil {
			fields = append(fields, observability.F("error", err.Error()))
		}
		logger.Info("use_case_done", fields...)
	}()

	if cmd.OrderID == "" {
		outcome, statusText = "error", "ORDER_ID_REQUIRED"
		return nil, errors.New("payment: order id is required")
	}
	if cmd.Amount < 0 {
		outcome, statusText = "error", "AMOUNT_INVALID"
		return nil, errors.New("payment: amount must be zero or greater")
	}

	order, err := uc.orderRepo.Get(ctx, cmd.OrderID)
	if err != nil {
		outcome, statusText = "error", "ORDER_LOOKUP_FAILED"
		return nil, err
	}

	if order.Status == domorder.StatusCompleted {
		outcome, statusText = "error", "ORDER_ALREADY_PAID"
		return nil, errors.New("payment: order already paid")
	}
	if !order.CanProcessPayment() {
		outcome, statusText = "error", "ORDER_NOT_READY"
		return nil, errors.New("payment: order not ready for payment")
	}
	if cmd.Amount > 0 {
		order.Amount = cmd.Amount
	}

	status, err = uc.pay(ctx, order.ID, order.Amount)
	result.Status = status
	if err != nil {
		outcome, statusText = "error", paymentSimulationFailed
		failureReason = err.Error()
		return result, err
	}

	switch status {
	case pstat.StatusSuccess:
		if transErr := order.PaymentSucceeded(); transErr != nil {
			outcome, statusText = "error", "STATE_TRANSITION_FAILED"
			failureReason = transErr.Error()
			result.Status = pstat.StatusFailed
			return result, transErr
		}
		statusText = "OK"
	default:
		failureReason = paymentDeclinedReason
		if transErr := order.PaymentFailed(paymentDeclinedReason); transErr != nil {
			outcome, statusText = "error", "STATE_TRANSITION_FAILED"
			failureReason = transErr.Error()
			result.Status = pstat.StatusFailed
			return result, transErr
		}
		statusText = "DECLINED"
	}

	if err = uc.orderRepo.Update(ctx, order); err != nil {
		outcome, statusText = "error", "ORDER_UPDATE_FAILED"
		failureReason = err.Error()
		return result, err
	}

	return result, nil
}

// ProcessPayment maintains the previous signature for callers not yet updated.
func (uc *ProcessPaymentUseCase) ProcessPayment(ctx context.Context, orderID string, amount int64) (pstat.Status, error) {
	res, err := uc.Execute(ctx, ProcessPaymentInput{OrderID: orderID, Amount: amount})
	if res == nil {
		return pstat.StatusFailed, err
	}
	return res.Status, err
}

// pay simulates the payment result.
func (uc *ProcessPaymentUseCase) pay(ctx context.Context, orderID string, amount int64) (pstat.Status, error) {
	uc.mu.Lock()
	defer uc.mu.Unlock()

	// respect cancellation even though this is mocked
	select {
	case <-ctx.Done():
		return pstat.StatusFailed, ctx.Err()
	default:
	}

	if uc.random.Float64() <= uc.successRate {
		return pstat.StatusSuccess, nil
	}

	return pstat.StatusFailed, nil
}

// SetSuccessRate adjusts the success rate for simulations (primarily for tests).
func (uc *ProcessPaymentUseCase) SetSuccessRate(rate float64) {
	uc.mu.Lock()
	if rate < 0 {
		rate = 0
	}
	if rate > 1 {
		rate = 1
	}
	uc.successRate = rate
	uc.mu.Unlock()
}
