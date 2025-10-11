package order

import (
	"errors"
	"time"
)

var (
	ErrNotFound        = errors.New("order: not found")
	ErrInvalidQuantity = errors.New("order: quantity must be greater than zero")
	ErrInvalidAmount   = errors.New("order: amount must be zero or greater")
)

type Status string

const (
	StatusPending       Status = "pending"
	StatusCompleted     Status = "completed"
	StatusPaymentFailed Status = "payment_failed"
)

type Order struct {
	ID         string
	CustomerID string
	ProductID  string
	Quantity   int
	Amount     int64
	Status     Status
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func New(id, customerID, productID string, quantity int, amount int64) (*Order, error) {
	if quantity <= 0 {
		return nil, ErrInvalidQuantity
	}
	if amount < 0 {
		return nil, ErrInvalidAmount
	}

	now := time.Now().UTC()
	return &Order{
		ID:         id,
		CustomerID: customerID,
		ProductID:  productID,
		Quantity:   quantity,
		Amount:     amount,
		Status:     StatusPending,
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}

func (o *Order) MarkCompleted() {
	o.Status = StatusCompleted
	o.touch()
}

func (o *Order) MarkPaymentFailed() {
	o.Status = StatusPaymentFailed
	o.touch()
}

func (o *Order) touch() {
	o.UpdatedAt = time.Now().UTC()
}
