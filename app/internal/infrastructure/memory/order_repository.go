package memory

import (
	"context"
	"fmt"
	"sync"

	domain "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/order"
)

type OrderRepository struct {
	mu          sync.RWMutex
	orders      map[string]*domain.Order
	idempotency map[string]string
}

func NewOrderRepository() *OrderRepository {
	return &OrderRepository{
		orders:      make(map[string]*domain.Order),
		idempotency: make(map[string]string),
	}
}

func (r *OrderRepository) Insert(ctx context.Context, order *domain.Order) error {
	_ = ctx
	if order == nil || order.ID == "" {
		return fmt.Errorf("order repository: id is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.orders[order.ID]; exists {
		return domain.ErrConflict
	}

	if key := order.IdempotencyKey; key != "" {
		if existingID, exists := r.idempotency[key]; exists {
			if _, ok := r.orders[existingID]; ok {
				return domain.ErrConflict
			}
		}
	}

	r.orders[order.ID] = cloneOrder(order)
	if key := order.IdempotencyKey; key != "" {
		r.idempotency[key] = order.ID
	}
	return nil
}

func (r *OrderRepository) Get(ctx context.Context, id string) (*domain.Order, error) {
	_ = ctx

	r.mu.RLock()
	defer r.mu.RUnlock()

	order, ok := r.orders[id]
	if !ok {
		return nil, domain.ErrNotFound
	}

	return cloneOrder(order), nil
}

func (r *OrderRepository) Update(ctx context.Context, order *domain.Order) error {
	_ = ctx
	if order == nil || order.ID == "" {
		return fmt.Errorf("order repository: id is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.orders[order.ID]; !exists {
		return domain.ErrNotFound
	}

	r.orders[order.ID] = cloneOrder(order)
	if key := order.IdempotencyKey; key != "" {
		r.idempotency[key] = order.ID
	}
	return nil
}

func (r *OrderRepository) FindByIdempotency(ctx context.Context, customerID, key string) (*domain.Order, error) {
	_ = ctx
	_ = customerID
	if key == "" {
		return nil, domain.ErrNotFound
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	orderID, ok := r.idempotency[key]
	if !ok {
		return nil, domain.ErrNotFound
	}

	order, found := r.orders[orderID]
	if !found {
		return nil, domain.ErrNotFound
	}

	return cloneOrder(order), nil
}

func cloneOrder(order *domain.Order) *domain.Order {
	if order == nil {
		return nil
	}
	clone := order.Clone()
	return clone
}
