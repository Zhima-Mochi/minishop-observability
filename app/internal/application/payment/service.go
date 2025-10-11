package payment

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"

	domain "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/payment"
)

type Service struct {
	mu          sync.Mutex
	random      *rand.Rand
	successRate float64
}

func NewService(successRate float64) *Service {
	if successRate <= 0 || successRate > 1 {
		successRate = 0.7
	}
	return &Service{
		random:      rand.New(rand.NewSource(time.Now().UnixNano())),
		successRate: successRate,
	}
}

func (s *Service) Pay(ctx context.Context, orderID string, amount int64) (domain.Status, error) {
	if orderID == "" {
		return domain.StatusFailed, errors.New("payment: order id is required")
	}
	if amount < 0 {
		return domain.StatusFailed, errors.New("payment: amount must be zero or greater")
	}

	_ = ctx

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.random.Float64() <= s.successRate {
		return domain.StatusSuccess, nil
	}

	return domain.StatusFailed, nil
}

func (s *Service) SuccessRate() float64 {
	return s.successRate
}
