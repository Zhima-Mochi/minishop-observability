package inventory

import (
	"context"
	"errors"

	domain "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/inventory"
)

type Service struct {
	repo         domain.Repository
	initialStock int
}

func NewService(repo domain.Repository, initialStock int) *Service {
	if initialStock < 0 {
		initialStock = 0
	}
	return &Service{
		repo:         repo,
		initialStock: initialStock,
	}
}

func (s *Service) Deduct(ctx context.Context, productID string, quantity int) (int, error) {
	if productID == "" {
		return 0, errors.New("inventory: product id is required")
	}

	item, err := s.repo.Get(ctx, productID)
	if errors.Is(err, domain.ErrNotFound) {
		item, err = domain.NewItem(productID, s.initialStock)
		if err != nil {
			return 0, err
		}
	} else if err != nil {
		return 0, err
	}

	if err := item.Deduct(quantity); err != nil {
		return 0, err
	}

	if err := s.repo.Save(ctx, item); err != nil {
		return 0, err
	}

	return item.Quantity, nil
}

func (s *Service) Get(ctx context.Context, productID string) (*domain.Item, error) {
	return s.repo.Get(ctx, productID)
}
