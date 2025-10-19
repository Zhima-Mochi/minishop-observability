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

type Service struct {
	mu          sync.Mutex
	random      *rand.Rand
	successRate float64
	orderRepo   domorder.Repository
	tel         observability.Telemetry
	log         observability.Logger
	reqCounter  observability.Counter
	durHist     observability.Histogram
}

func NewService(orderRepo domorder.Repository, tel observability.Telemetry) *Service {
	baseLog := observability.NopLogger()
	var req observability.Counter
	var dur observability.Histogram
	if tel != nil {
		baseLog = tel.Logger().With(
			observability.F("service", paymentService),
		)
		req = tel.Counter("usecase_requests_total")
		dur = tel.Histogram("usecase_duration_seconds")
	}

	return &Service{
		random:      rand.New(rand.NewSource(time.Now().UnixNano())),
		successRate: defaultPaymentSuccess,
		orderRepo:   orderRepo,
		tel:         tel,
		log:         baseLog,
		reqCounter:  req,
		durHist:     dur,
	}
}

// ProcessPayment checks order existence and status, then simulates payment and updates order state.
func (s *Service) ProcessPayment(ctx context.Context, orderID string, amount int64) (status pstat.Status, err error) {
	logger := logctx.FromOr(ctx, s.log).With(
		observability.F("use_case", useCasePaymentProcess),
		observability.F("order_id", orderID),
		observability.F("amount", amount),
	)

	tracer := observability.NopTracer()
	if s.tel != nil {
		tracer = s.tel.Tracer()
	}

	ctx, span := tracer.Start(ctx, spanPrefix+paymentSpanName,
		attribute.String("use_case", useCasePaymentProcess),
		attribute.String("order.id", orderID),
		attribute.Int64("payment.amount_requested", amount),
	)
	start := time.Now()
	outcome, statusText := "success", "OK"
	status = pstat.StatusFailed

	defer func() {
		if span != nil {
			span.SetAttributes(
				attribute.String("payment.status", string(status)),
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
		if s.reqCounter != nil {
			s.reqCounter.Add(1,
				observability.L("use_case", useCasePaymentProcess),
				observability.L("outcome", outcome),
			)
		}
		if s.durHist != nil {
			s.durHist.Observe(latency,
				observability.L("use_case", useCasePaymentProcess),
			)
		}

		fields := []observability.Field{
			observability.F("outcome", outcome),
			observability.F("status", statusText),
			observability.F("latency_seconds", latency),
			observability.F("order_id", orderID),
			observability.F("amount", amount),
			observability.F("payment_status", string(status)),
		}
		if err != nil {
			fields = append(fields, observability.F("error", err.Error()))
		}
		logger.Info("use_case_done", fields...)
	}()

	if orderID == "" {
		outcome, statusText = "error", "ORDER_ID_REQUIRED"
		return status, errors.New("payment: order id is required")
	}
	if amount < 0 {
		outcome, statusText = "error", "AMOUNT_INVALID"
		return status, errors.New("payment: amount must be zero or greater")
	}

	order, err := s.orderRepo.Get(ctx, orderID)
	if err != nil {
		outcome, statusText = "error", "ORDER_LOOKUP_FAILED"
		return status, err
	}
	if order.Status == domorder.StatusCompleted {
		outcome, statusText = "error", "ORDER_ALREADY_PAID"
		return status, errors.New("payment: order already paid")
	}
	if !order.CanProcessPayment() {
		outcome, statusText = "error", "ORDER_NOT_READY"
		return status, errors.New("payment: order not ready for payment")
	}
	if amount > 0 {
		order.Amount = amount
	}

	status, err = s.pay(ctx, order.ID, order.Amount)
	if err != nil {
		outcome, statusText = "error", paymentSimulationFailed
		logger.Error("payment_error",
			observability.F("error", err.Error()),
		)
		return status, err
	}

	switch status {
	case pstat.StatusSuccess:
		if err := order.PaymentSucceeded(); err != nil {
			outcome, statusText = "error", "STATE_TRANSITION_FAILED"
			logger.Error("payment_state_transition_failed",
				observability.F("error", err.Error()),
			)
			return pstat.StatusFailed, err
		}
		statusText = "OK"
	default:
		if err := order.PaymentFailed(paymentDeclinedReason); err != nil {
			outcome, statusText = "error", "STATE_TRANSITION_FAILED"
			logger.Error("payment_state_transition_failed",
				observability.F("error", err.Error()),
			)
			return pstat.StatusFailed, err
		}
		statusText = "DECLINED"
	}

	if err := s.orderRepo.Update(ctx, order); err != nil {
		outcome, statusText = "error", "ORDER_UPDATE_FAILED"
		logger.Error("order_update_failed",
			observability.F("error", err.Error()),
		)
		return status, err
	}

	return status, nil
}

// pay simulates the payment result.
func (s *Service) pay(ctx context.Context, orderID string, amount int64) (pstat.Status, error) {
	if orderID == "" {
		return pstat.StatusFailed, errors.New("payment: order id is required")
	}
	if amount < 0 {
		return pstat.StatusFailed, errors.New("payment: amount must be zero or greater")
	}
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.random.Float64() <= s.successRate {
		return pstat.StatusSuccess, nil
	}
	return pstat.StatusFailed, nil
}

func (s *Service) SuccessRate() float64 { return s.successRate }
