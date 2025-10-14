package worker

import (
	"context"

	appInventory "github.com/Zhima-Mochi/minishop-observability/app/internal/application/inventory"
	domorder "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/order"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/infrastructure/outbox"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/pkg/logging"
)

type Worker struct {
	subscriber outbox.Subscriber
	service    *appInventory.Service
}

func New(subscriber outbox.Subscriber, service *appInventory.Service) *Worker {
	return &Worker{
		subscriber: subscriber,
		service:    service,
	}
}

func (w *Worker) Start() {
	w.subscriber.Subscribe(domorder.OrderCreatedEvent{}.EventName(), w.handleOrderCreated)
}

func (w *Worker) handleOrderCreated(ctx context.Context, e outbox.Event) error {
	logger := logging.FromContext(ctx).With("component", "inventory_worker")
	evt, ok := e.(domorder.OrderCreatedEvent)
	if !ok {
		return nil
	}

	if err := w.service.OnOrderCreated(ctx, evt); err != nil {
		logger.Warn("inventory_reservation_failed", "order_id", evt.OrderID, "product_id", evt.ProductID, "error", err)
		return err
	}

	logger.Info("inventory_reservation_succeeded", "order_id", evt.OrderID, "product_id", evt.ProductID, "quantity", evt.Quantity)
	return nil
}
