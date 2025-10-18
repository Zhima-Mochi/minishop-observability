package inventory

import (
	"context"
)

type Repository interface {
	Reserve(ctx context.Context, productID string, quantity int) error
}
