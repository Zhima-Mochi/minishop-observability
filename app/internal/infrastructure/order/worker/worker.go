package worker

import (
	"context"
	"fmt"
	"log/slog"

	dominventory "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/inventory"
	domorder "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/order"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/infrastructure/outbox"
)

type Worker struct {
	repo       domorder.Repository
	subscriber outbox.Subscriber
	publisher  outbox.Publisher
	log        *slog.Logger
}

func New(repo domorder.Repository, subscriber outbox.Subscriber, publisher outbox.Publisher, logger *slog.Logger) *Worker {
	return &Worker{
		repo:       repo,
		subscriber: subscriber,
		publisher:  publisher,
		log:        logger,
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

	order, err := w.repo.Get(ctx, evt.OrderID)
	if err != nil {
		w.logError("order_load_failed", evt.OrderID, err)
		return fmt.Errorf("order worker: find order: %w", err)
	}

	if err := order.InventoryReserved(); err != nil {
		w.logError("order_state_transition_failed", evt.OrderID, err)
		return fmt.Errorf("order worker: inventory reserved transition: %w", err)
	}

	if err := w.repo.Update(ctx, order); err != nil {
		w.logError("order_update_failed", evt.OrderID, err)
		return fmt.Errorf("order worker: update order: %w", err)
	}

	if w.publisher != nil {
		if err := w.publisher.Publish(ctx, domorder.NewOrderInventoryReservedEvent(order)); err != nil && w.log != nil {
			w.log.Warn("order_inventory_reserved_event_publish_failed", "order_id", order.ID, "error", err)
		}
	}

	if w.log != nil {
		w.log.Info("order_inventory_reserved", "order_id", order.ID)
	}
	return nil
}

func (w *Worker) handleInventoryReservationFailed(ctx context.Context, e outbox.Event) error {
	evt, ok := e.(dominventory.InventoryReservationFailedEvent)
	if !ok {
		return nil
	}

	order, err := w.repo.Get(ctx, evt.OrderID)
	if err != nil {
		w.logError("order_load_failed", evt.OrderID, err)
		return fmt.Errorf("order worker: find order: %w", err)
	}

	if err := order.InventoryReservationFailed(evt.Reason); err != nil {
		w.logError("order_state_transition_failed", evt.OrderID, err)
		return fmt.Errorf("order worker: inventory reservation failed transition: %w", err)
	}

	if err := w.repo.Update(ctx, order); err != nil {
		w.logError("order_update_failed", evt.OrderID, err)
		return fmt.Errorf("order worker: update order: %w", err)
	}

	if w.publisher != nil {
		if err := w.publisher.Publish(ctx, domorder.NewOrderInventoryReservationFailedEvent(order, evt.Reason)); err != nil && w.log != nil {
			w.log.Warn("order_inventory_failed_event_publish_failed", "order_id", order.ID, "error", err)
		}
	}

	if w.log != nil {
		w.log.Warn("order_inventory_reservation_failed", "order_id", order.ID, "reason", evt.Reason)
	}
	return nil
}

func (w *Worker) logError(msg string, orderID string, err error) {
	if w.log != nil {
		w.log.Error(msg, "order_id", orderID, "error", err)
	}
}
