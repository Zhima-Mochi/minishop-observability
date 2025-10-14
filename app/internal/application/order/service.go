package order

import (
	"context"
	"errors"
	"fmt"

	domain "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/order"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/infrastructure/outbox"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/pkg/logging"
)

type Service struct {
	repo        domain.Repository
	idGenerator IDGenerator
	publisher   outbox.Publisher
}

func NewService(repo domain.Repository, idGen IDGenerator, publisher outbox.Publisher) *Service {
	return &Service{
		repo:        repo,
		idGenerator: idGen,
		publisher:   publisher,
	}
}

type CreateOrderInput struct {
	CustomerID string
	ProductID  string
	Quantity   int
	Amount     int64
}

type CreateOrderResult struct {
	OrderID string
	Status  domain.Status
}

func (s *Service) CreateOrder(ctx context.Context, input CreateOrderInput) (*CreateOrderResult, error) {
	logger := logging.FromContext(ctx).With("component", "order_service")
	logger.Info("create_order_start", "customer_id", input.CustomerID, "product_id", input.ProductID, "qty", input.Quantity, "amount", input.Amount)
	if input.CustomerID == "" {
		return nil, errors.New("order: customer id is required")
	}
	if input.ProductID == "" {
		return nil, errors.New("order: product id is required")
	}

	orderID := s.idGenerator.NewID()

	entity, err := domain.New(orderID, input.CustomerID, input.ProductID, input.Quantity, input.Amount)
	if err != nil {
		return nil, err
	}

	if err := s.repo.Save(ctx, entity); err != nil {
		logger.Error("order_save_failed", "order_id", entity.ID, "error", err)
		return nil, fmt.Errorf("order: save: %w", err)
	}

	// Publish domain event for inventory to handle asynchronously (Outbox/Event Bus)
	if s.publisher != nil {
		evt := domain.NewOrderCreatedEvent(entity)
		if err := s.publisher.Publish(ctx, evt); err != nil {
			logger.Warn("event_publish_failed", "order_id", entity.ID, "event", evt.EventName(), "error", err)
			// Do not fail order creation due to downstream context failure
		}
	}

	logger.Info("create_order_success", "order_id", entity.ID, "status", entity.Status)
	return &CreateOrderResult{
		OrderID: entity.ID,
		Status:  entity.Status,
	}, nil
}

func (s *Service) Get(ctx context.Context, id string) (*domain.Order, error) {
	if id == "" {
		return nil, errors.New("order: id is required")
	}
	order, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return order, nil
}

// Payment is handled by the payment service; order service does not process payments.
