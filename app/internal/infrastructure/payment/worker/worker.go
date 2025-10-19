package worker

import (
	"context"

	appPayment "github.com/Zhima-Mochi/minishop-observability/app/internal/application/payment"
	domorder "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/order"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/infrastructure/outbox"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/pkg/logging"
	"go.uber.org/zap"
)

type Worker struct {
	subscriber outbox.Subscriber
	service    *appPayment.Service
}

func New(subscriber outbox.Subscriber, service *appPayment.Service) *Worker {
	return &Worker{
		subscriber: subscriber,
		service:    service,
	}
}

func (w *Worker) Start() {
	w.subscriber.Subscribe(domorder.OrderInventoryReservedEvent{}.EventName(), w.handleOrderInventoryReserved)
}

func (w *Worker) handleOrderInventoryReserved(ctx context.Context, e outbox.Event) error {

	logger := logging.FromContext(ctx).With(zap.String("component", "payment_worker"))

	evt, ok := e.(domorder.OrderInventoryReservedEvent)
	if !ok {
		return nil
	}

	status, err := w.service.ProcessPayment(ctx, evt.OrderID, 0)
	if err != nil {
		logger.Warn("payment_processing_failed",
			zap.String("order_id", evt.OrderID),
			zap.Error(err),
		)
		return err
	}

	logger.Info("payment_processed",
		zap.String("order_id", evt.OrderID),
		zap.String("status", string(status)),
	)
	return nil
}
