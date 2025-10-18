package memory

import (
	"context"
	"sync"
	"time"

	domain "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/inventory"
)

type InventoryRepository struct {
	mu    sync.Mutex
	items map[string]*domain.Item
}

func NewInventoryRepository() *InventoryRepository {
	return &InventoryRepository{
		items: make(map[string]*domain.Item),
	}
}

func (r *InventoryRepository) Reserve(ctx context.Context, productID string, quantity int) error {
	_ = ctx

	if productID == "" {
		return domain.ErrNotFound
	}
	if quantity <= 0 {
		return domain.ErrInvalidQuantity
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	item, ok := r.items[productID]
	if !ok {
		return domain.ErrNotFound
	}
	if quantity > item.Quantity {
		return domain.ErrInsufficientStock
	}

	item.Quantity -= quantity
	item.UpdatedAt = time.Now().UTC()
	return nil
}

// Seed allows tests or bootstrap code to populate inventory quantities directly.
func (r *InventoryRepository) Seed(productID string, quantity int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[productID] = &domain.Item{
		ProductID: productID,
		Quantity:  quantity,
		UpdatedAt: time.Now().UTC(),
	}
}
