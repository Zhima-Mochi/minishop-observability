package order

import (
    "context"
    "errors"
    "fmt"

    dominv "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/inventory"
    domain "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/order"
    "github.com/Zhima-Mochi/minishop-observability/app/internal/pkg/logging"
)

type Service struct {
    repo         domain.Repository
    invRepo      dominv.Repository
    initialStock int
    idGenerator  IDGenerator
}

func NewService(repo domain.Repository, invRepo dominv.Repository, idGen IDGenerator, initialStock int) *Service {
    return &Service{
        repo:         repo,
        invRepo:      invRepo,
        initialStock: initialStock,
        idGenerator:  idGen,
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

	// Inventory deduction is done directly via repository (no application layer)
	if _, err := s.deductInventory(ctx, entity.ProductID, entity.Quantity); err != nil {
		logger.Error("inventory_deduct_failed", "order_id", entity.ID, "product_id", entity.ProductID, "error", err)
		return nil, fmt.Errorf("order: inventory deduction failed: %w", err)
	}

    logger.Info("create_order_success", "order_id", entity.ID, "status", entity.Status)
    return &CreateOrderResult{
        OrderID: entity.ID,
        Status:  entity.Status,
    }, nil
}

// deductInventory loads or initializes inventory for the product and deducts quantity.
func (s *Service) deductInventory(ctx context.Context, productID string, quantity int) (int, error) {
	if productID == "" {
		return 0, errors.New("inventory: product id is required")
	}

	item, err := s.invRepo.Get(ctx, productID)
	if errors.Is(err, dominv.ErrNotFound) {
		item, err = dominv.NewItem(productID, s.initialStock)
		if err != nil {
			return 0, err
		}
	} else if err != nil {
		return 0, err
	}

	if err := item.Deduct(quantity); err != nil {
		return 0, err
	}

	if err := s.invRepo.Save(ctx, item); err != nil {
		return 0, err
	}

	return item.Quantity, nil
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
