package inventory

import (
	"context"
	"errors"
	"fmt"

	dominv "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/inventory"
	domorder "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/order"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/infrastructure/outbox"
)

type Service struct {
	invRepo   dominv.Repository
	publisher outbox.Publisher
}

func NewService(invRepo dominv.Repository, publisher outbox.Publisher) *Service {
	return &Service{
		invRepo:   invRepo,
		publisher: publisher,
	}
}

// OnOrderCreated reacts to OrderCreated events and emits reservation result events.
func (s *Service) OnOrderCreated(ctx context.Context, e domorder.OrderCreatedEvent) error {
	if err := s.invRepo.Reserve(ctx, e.ProductID, e.Quantity); err != nil {
		reason := dominv.FailureReasonInsufficientStock
		switch {
		case errors.Is(err, dominv.ErrNotFound):
			reason = dominv.FailureReasonNotFound
		case errors.Is(err, dominv.ErrInvalidQuantity):
			reason = dominv.FailureReasonPersistenceError
		case !errors.Is(err, dominv.ErrInsufficientStock):
			reason = err.Error()
		}
		_ = s.publishFailure(ctx, e, reason)
		return fmt.Errorf("inventory: reserve: %w", err)
	}

	if err := s.publisher.Publish(ctx, dominv.NewInventoryReservedEvent(e.OrderID, e.ProductID, e.Quantity)); err != nil {
		return fmt.Errorf("inventory: publish reserved: %w", err)
	}

	return nil
}

func (s *Service) publishFailure(ctx context.Context, e domorder.OrderCreatedEvent, reason string) error {
	return s.publisher.Publish(ctx, dominv.NewInventoryReservationFailedEvent(e.OrderID, e.ProductID, e.Quantity, reason))
}
