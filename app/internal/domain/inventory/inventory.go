package inventory

import (
	"errors"
	"time"
)

var (
	ErrNotFound          = errors.New("inventory: product not found")
	ErrInvalidQuantity   = errors.New("inventory: quantity must be greater than zero")
	ErrInsufficientStock = errors.New("inventory: insufficient stock")
)

type Item struct {
	ProductID string
	Quantity  int
	UpdatedAt time.Time
}

func NewItem(productID string, quantity int) (*Item, error) {
	if quantity < 0 {
		return nil, ErrInvalidQuantity
	}
	return &Item{
		ProductID: productID,
		Quantity:  quantity,
		UpdatedAt: time.Now().UTC(),
	}, nil
}

func (i *Item) Deduct(quantity int) error {
	if quantity <= 0 {
		return ErrInvalidQuantity
	}
	if quantity > i.Quantity {
		return ErrInsufficientStock
	}
	i.Quantity -= quantity
	i.touch()
	return nil
}

func (i *Item) touch() {
	i.UpdatedAt = time.Now().UTC()
}
