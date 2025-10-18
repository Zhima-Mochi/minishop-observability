package payment

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"

	domorder "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/order"
	pstat "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/payment"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/pkg/logging"
)

type Service struct {
	mu          sync.Mutex
	random      *rand.Rand
	successRate float64
	orderRepo   domorder.Repository
}

func NewService(orderRepo domorder.Repository) *Service {
	return &Service{
		random:      rand.New(rand.NewSource(time.Now().UnixNano())),
		successRate: 0.7,
		orderRepo:   orderRepo,
	}
}

// ProcessPayment checks order existence and status, then simulates payment and updates order state.
func (s *Service) ProcessPayment(ctx context.Context, orderID string, amount int64) (pstat.Status, error) {
	logger := logging.FromContext(ctx).With("component", "payment_service")
	logger.Info("process_payment_start", "order_id", orderID, "amount", amount)

	if orderID == "" {
		return pstat.StatusFailed, errors.New("payment: order id is required")
	}
	if amount < 0 {
		return pstat.StatusFailed, errors.New("payment: amount must be zero or greater")
	}

	order, err := s.orderRepo.Get(ctx, orderID)
	if err != nil {
		return pstat.StatusFailed, err
	}
	if order.Status == domorder.StatusCompleted {
		return pstat.StatusFailed, errors.New("payment: order already paid")
	}
	if !order.CanProcessPayment() {
		return pstat.StatusFailed, errors.New("payment: order not ready for payment")
	}
	if amount > 0 {
		order.Amount = amount
	}

	status, err := s.pay(ctx, order.ID, order.Amount)
	if err != nil {
		logger.Error("payment_error", "order_id", order.ID, "error", err)
		return pstat.StatusFailed, err
	}

	switch status {
	case pstat.StatusSuccess:
		if err := order.PaymentSucceeded(); err != nil {
			logger.Error("payment_state_transition_failed", "order_id", order.ID, "error", err)
			return pstat.StatusFailed, err
		}
		logger.Info("payment_success", "order_id", order.ID)
	default:
		if err := order.PaymentFailed("payment_declined"); err != nil {
			logger.Error("payment_state_transition_failed", "order_id", order.ID, "error", err)
			return pstat.StatusFailed, err
		}
		logger.Info("payment_failed", "order_id", order.ID)
	}

	if err := s.orderRepo.Update(ctx, order); err != nil {
		logger.Error("order_update_failed", "order_id", order.ID, "error", err)
		return pstat.StatusFailed, err
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
