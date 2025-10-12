package payment

import (
	"context"

	dompay "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/payment"
)

// Processor is an outbound port for payment capability.
// It belongs to the application layer to express use-case dependencies.
type Processor interface {
	Pay(ctx context.Context, orderID string, amount int64) (dompay.Status, error)
}
