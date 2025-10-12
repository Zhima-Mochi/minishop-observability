package memory

import (
	"context"
	"sync"

	domain "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/inventory"
)

type InventoryRepository struct {
	mu    sync.RWMutex
	items map[string]*domain.Item
}

func NewInventoryRepository() *InventoryRepository {
	return &InventoryRepository{
		items: make(map[string]*domain.Item),
	}
}

func (r *InventoryRepository) Get(ctx context.Context, productID string) (*domain.Item, error) {
	_ = ctx

	r.mu.RLock()
	defer r.mu.RUnlock()

	if _, ok := r.items[productID]; !ok {
		r.items[productID] = &domain.Item{
			ProductID: productID,
			Quantity:  1,
		}
	}

	return cloneItem(r.items[productID]), nil
}

func (r *InventoryRepository) Save(ctx context.Context, item *domain.Item) error {
	_ = ctx
	if item == nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.items[item.ProductID] = cloneItem(item)
	return nil
}

func cloneItem(item *domain.Item) *domain.Item {
	if item == nil {
		return nil
	}
	clone := *item
	return &clone
}
