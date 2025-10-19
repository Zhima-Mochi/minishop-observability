package worker

import (
	"context"
	"fmt"

	dominventory "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/inventory"
	domorder "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/order"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/infrastructure/outbox"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/pkg/logging"
	"go.uber.org/zap"
)

type Worker struct {
	repo       domorder.Repository
	subscriber outbox.Subscriber
	publisher  outbox.Publisher
}

func New(repo domorder.Repository, subscriber outbox.Subscriber, publisher outbox.Publisher) *Worker {
	return &Worker{
		repo:       repo,
		subscriber: subscriber,
		publisher:  publisher,
	}
}

func (w *Worker) Start() {
	if w.subscriber == nil || w.repo == nil {
		return
	}
	w.subscriber.Subscribe(dominventory.InventoryReservedEvent{}.EventName(), w.handleInventoryReserved)
	w.subscriber.Subscribe(dominventory.InventoryReservationFailedEvent{}.EventName(), w.handleInventoryReservationFailed)
}

func (w *Worker) handleInventoryReserved(ctx context.Context, e outbox.Event) error {
	evt, ok := e.(dominventory.InventoryReservedEvent)
	if !ok {
		return nil
	}

	logger := logging.FromContext(ctx).With(zap.String("component", "order_worker"))

	order, err := w.repo.Get(ctx, evt.OrderID)
	if err != nil {
		logger.Warn("order_load_failed", zap.String("order_id", evt.OrderID), zap.Error(err))
		return fmt.Errorf("order worker: find order: %w", err)
	}

	if err := order.InventoryReserved(); err != nil {
		logger.Warn("order_state_transition_failed", zap.String("order_id", evt.OrderID), zap.Error(err))
		return fmt.Errorf("order worker: inventory reserved transition: %w", err)
	}

	if err := w.repo.Update(ctx, order); err != nil {
		logger.Warn("order_update_failed", zap.String("order_id", evt.OrderID), zap.Error(err))
		return fmt.Errorf("order worker: update order: %w", err)
	}

	if w.publisher != nil {
		if err := w.publisher.Publish(ctx, domorder.NewOrderInventoryReservedEvent(order)); err != nil {
			logger.Warn("order_inventory_reserved_event_publish_failed",
				zap.String("order_id", order.ID),
				zap.Error(err),
			)
		}
	}

	logger.Info("order_inventory_reserved", zap.String("order_id", order.ID))
	return nil
}

func (w *Worker) handleInventoryReservationFailed(ctx context.Context, e outbox.Event) error {
	evt, ok := e.(dominventory.InventoryReservationFailedEvent)
	if !ok {
		return nil
	}

	logger := logging.FromContext(ctx).With(zap.String("component", "order_worker"))

	order, err := w.repo.Get(ctx, evt.OrderID)
	if err != nil {
		logger.Warn("order_load_failed", zap.String("order_id", evt.OrderID), zap.Error(err))
		return fmt.Errorf("order worker: find order: %w", err)
	}

	if err := order.InventoryReservationFailed(evt.Reason); err != nil {
		logger.Warn("order_state_transition_failed", zap.String("order_id", evt.OrderID), zap.Error(err))
		return fmt.Errorf("order worker: inventory reservation failed transition: %w", err)
	}

	if err := w.repo.Update(ctx, order); err != nil {
		logger.Warn("order_update_failed", zap.String("order_id", evt.OrderID), zap.Error(err))
		return fmt.Errorf("order worker: update order: %w", err)
	}

	if w.publisher != nil {
		if err := w.publisher.Publish(ctx, domorder.NewOrderInventoryReservationFailedEvent(order, evt.Reason)); err != nil {
			logger.Warn("order_inventory_failed_event_publish_failed",
				zap.String("order_id", order.ID),
				zap.Error(err),
			)
		}
	}

	logger.Warn("order_inventory_reservation_failed",
		zap.String("order_id", order.ID),
		zap.String("reason", evt.Reason),
	)
	return nil
}
