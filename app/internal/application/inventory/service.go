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
	item, err := s.invRepo.Get(ctx, e.ProductID)
	if err != nil {
		_ = s.publishFailure(ctx, e, dominv.FailureReasonNotFound)
		return fmt.Errorf("inventory: get: %w", err)
	}

	if err := item.Deduct(e.Quantity); err != nil {
		reason := dominv.FailureReasonInsufficientStock
		if !errors.Is(err, dominv.ErrInsufficientStock) {
			reason = err.Error()
		}
		_ = s.publishFailure(ctx, e, reason)
		return fmt.Errorf("inventory: deduct: %w", err)
	}

	if err := s.invRepo.Save(ctx, item); err != nil {
		_ = s.publishFailure(ctx, e, dominv.FailureReasonPersistenceError)
		return fmt.Errorf("inventory: save: %w", err)
	}

	if s.publisher != nil {
		if err := s.publisher.Publish(ctx, dominv.NewInventoryReservedEvent(e.OrderID, e.ProductID, e.Quantity)); err != nil {
			return fmt.Errorf("inventory: publish reserved: %w", err)
		}
	}

	return nil
}

func (s *Service) publishFailure(ctx context.Context, e domorder.OrderCreatedEvent, reason string) error {
	if s.publisher == nil {
		return nil
	}
	return s.publisher.Publish(ctx, dominv.NewInventoryReservationFailedEvent(e.OrderID, e.ProductID, e.Quantity, reason))
}
