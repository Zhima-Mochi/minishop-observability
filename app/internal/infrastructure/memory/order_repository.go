package memory

import (
	"context"
	"fmt"
	"sync"

	domain "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/order"
)

type OrderRepository struct {
	mu     sync.RWMutex
	orders map[string]*domain.Order
}

func NewOrderRepository() *OrderRepository {
	return &OrderRepository{
		orders: make(map[string]*domain.Order),
	}
}

func (r *OrderRepository) Save(ctx context.Context, order *domain.Order) error {
	_ = ctx
	if order == nil {
		return fmt.Errorf("order repository: order is nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.orders[order.ID]; exists {
		return fmt.Errorf("order repository: order %s already exists", order.ID)
	}

	r.orders[order.ID] = cloneOrder(order)
	return nil
}

func (r *OrderRepository) FindByID(ctx context.Context, id string) (*domain.Order, error) {
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
	if order == nil {
		return fmt.Errorf("order repository: order is nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.orders[order.ID]; !exists {
		return domain.ErrNotFound
	}

	r.orders[order.ID] = cloneOrder(order)
	return nil
}

func cloneOrder(order *domain.Order) *domain.Order {
	if order == nil {
		return nil
	}
	return order.Clone()
}
