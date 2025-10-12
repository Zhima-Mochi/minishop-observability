package order

import (
	"context"

	payment "github.com/Zhima-Mochi/minishop-observability/app/internal/application/payment"
)

type IDGenerator interface {
	NewID() string
}

type InventoryPort interface {
	Deduct(ctx context.Context, productID string, quantity int) (int, error)
}

type PaymentPort interface{ payment.Processor }
