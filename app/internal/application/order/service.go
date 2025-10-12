package order

import (
	"context"
	"errors"
	"fmt"

	domain "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/order"
	domainPayment "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/payment"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/pkg/logging"
)

type Service struct {
	repo        domain.Repository
	inventory   InventoryPort
	payment     PaymentPort
	idGenerator IDGenerator
}

func NewService(repo domain.Repository, inventory InventoryPort, payment PaymentPort, idGen IDGenerator) *Service {
	return &Service{
		repo:        repo,
		inventory:   inventory,
		payment:     payment,
		idGenerator: idGen,
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

	if _, err := s.inventory.Deduct(ctx, entity.ProductID, entity.Quantity); err != nil {
		logger.Error("inventory_deduct_failed", "order_id", entity.ID, "product_id", entity.ProductID, "error", err)
		return nil, fmt.Errorf("order: inventory deduction failed: %w", err)
	}

	_, err = s.executePayment(ctx, entity)
	if err != nil {
		logger.Error("order_payment_failed", "order_id", entity.ID, "error", err)
		return nil, err
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

func (s *Service) ProcessPayment(ctx context.Context, id string, amount int64) (domainPayment.Status, error) {
	logger := logging.FromContext(ctx).With("component", "order_service")
	logger.Info("process_payment_start", "order_id", id, "amount", amount)
	order, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return domainPayment.StatusFailed, err
	}
	if amount > 0 {
		order.Amount = amount
	}
	return s.executePayment(ctx, order)
}

func (s *Service) executePayment(ctx context.Context, entity *domain.Order) (domainPayment.Status, error) {
	logger := logging.FromContext(ctx).With("component", "order_service")
	status, err := s.payment.Pay(ctx, entity.ID, entity.Amount)
	if err != nil {
		logger.Error("payment_error", "order_id", entity.ID, "error", err)
		return domainPayment.StatusFailed, fmt.Errorf("order: payment failed: %w", err)
	}

	switch status {
	case domainPayment.StatusSuccess:
		entity.MarkCompleted()
		logger.Info("payment_success", "order_id", entity.ID)
	default:
		entity.MarkPaymentFailed()
		logger.Info("payment_failed", "order_id", entity.ID)
	}

	if err := s.repo.Update(ctx, entity); err != nil {
		logger.Error("order_update_failed", "order_id", entity.ID, "error", err)
		return domainPayment.StatusFailed, fmt.Errorf("order: update: %w", err)
	}

	return status, nil
}
