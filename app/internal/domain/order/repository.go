package order

import "context"

type Repository interface {
	Save(ctx context.Context, order *Order) error
	FindByID(ctx context.Context, id string) (*Order, error)
	Update(ctx context.Context, order *Order) error
}
