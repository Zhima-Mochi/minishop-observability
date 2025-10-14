package order

import (
	"errors"
	"time"
)

var (
	ErrNotFound               = errors.New("order: not found")
	ErrInvalidQuantity        = errors.New("order: quantity must be greater than zero")
	ErrInvalidAmount          = errors.New("order: amount must be zero or greater")
	ErrInvalidStateTransition = errors.New("order: invalid state transition")
)

type Status string

const (
	StatusPending           Status = "pending"            // awaiting inventory reservation
	StatusInventoryReserved Status = "inventory_reserved" // inventory confirmed, awaiting payment
	StatusInventoryFailed   Status = "inventory_failed"   // inventory reservation failed
	StatusCompleted         Status = "completed"
	StatusPaymentFailed     Status = "payment_failed"
)

type Order struct {
	ID            string
	CustomerID    string
	ProductID     string
	Quantity      int
	Amount        int64
	Status        Status
	FailureReason string
	CreatedAt     time.Time
	UpdatedAt     time.Time

	state OrderState
}

func New(id, customerID, productID string, quantity int, amount int64) (*Order, error) {
	if quantity <= 0 {
		return nil, ErrInvalidQuantity
	}
	if amount < 0 {
		return nil, ErrInvalidAmount
	}

	now := time.Now().UTC()
	order := &Order{
		ID:         id,
		CustomerID: customerID,
		ProductID:  productID,
		Quantity:   quantity,
		Amount:     amount,
		Status:     StatusPending,
		CreatedAt:  now,
		UpdatedAt:  now,
		state:      pendingState{},
	}
	return order, nil
}

func (o *Order) Clone() *Order {
	if o == nil {
		return nil
	}
	clone := *o
	clone.state = nil
	clone.ensureState()
	return &clone
}

func (o *Order) InventoryReserved() error {
	o.ensureState()
	next, err := o.state.OnInventoryReserved(o)
	return o.transition(next, err)
}

func (o *Order) InventoryReservationFailed(reason string) error {
	o.ensureState()
	next, err := o.state.OnInventoryFailed(o, reason)
	return o.transition(next, err)
}

func (o *Order) PaymentSucceeded() error {
	o.ensureState()
	next, err := o.state.OnPaymentSucceeded(o)
	return o.transition(next, err)
}

func (o *Order) PaymentFailed(reason string) error {
	o.ensureState()
	next, err := o.state.OnPaymentFailed(o, reason)
	return o.transition(next, err)
}

func (o *Order) CanProcessPayment() bool {
	switch o.Status {
	case StatusInventoryReserved, StatusPaymentFailed:
		return true
	default:
		return false
	}
}

func (o *Order) transition(next OrderState, err error) error {
	if err != nil {
		return err
	}
	if next == nil {
		return ErrInvalidStateTransition
	}
	o.state = next
	o.Status = next.Status()
	o.touch()
	return nil
}

func (o *Order) ensureState() {
	if o.state != nil {
		return
	}
	switch o.Status {
	case StatusInventoryReserved:
		o.state = inventoryReservedState{}
	case StatusInventoryFailed:
		o.state = inventoryFailedState{}
	case StatusCompleted:
		o.state = completedState{}
	case StatusPaymentFailed:
		o.state = paymentFailedState{}
	default:
		o.state = pendingState{}
	}
}

func (o *Order) touch() {
	o.UpdatedAt = time.Now().UTC()
}
