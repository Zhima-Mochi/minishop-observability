package order

import "context"

type Repository interface {
	Insert(ctx context.Context, order *Order) error
	Get(ctx context.Context, id string) (*Order, error)
	Update(ctx context.Context, order *Order) error
	FindByIdempotency(ctx context.Context, customerID, key string) (*Order, error)
}
