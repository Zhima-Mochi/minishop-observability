package payment

import "context"

type Processor interface {
	Pay(ctx context.Context, orderID string, amount int64) (Status, error)
}
