package inventory

import (
	"context"
)

type Repository interface {
	Get(ctx context.Context, productID string) (*Item, error)
	Save(ctx context.Context, item *Item) error
}
