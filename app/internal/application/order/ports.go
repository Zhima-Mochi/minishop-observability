package order

import (
	"context"

	domainPayment "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/payment"
)

type IDGenerator interface {
	NewID() string
}

type InventoryPort interface {
	Deduct(ctx context.Context, productID string, quantity int) (int, error)
}

type PaymentPort interface {
	domainPayment.Processor
}
