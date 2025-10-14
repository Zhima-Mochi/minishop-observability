package inventory

import "time"

const (
	FailureReasonNotFound          = "not_found"
	FailureReasonInsufficientStock = "insufficient_stock"
	FailureReasonPersistenceError  = "persist_error"
)

// InventoryReservedEvent is emitted when stock is successfully reserved for an order.
type InventoryReservedEvent struct {
	OrderID    string
	ProductID  string
	Quantity   int
	OccurredAt time.Time
}

func (InventoryReservedEvent) EventName() string { return "inventory.reserved" }

func NewInventoryReservedEvent(orderID, productID string, quantity int) InventoryReservedEvent {
	return InventoryReservedEvent{
		OrderID:    orderID,
		ProductID:  productID,
		Quantity:   quantity,
		OccurredAt: time.Now().UTC(),
	}
}

// InventoryReservationFailedEvent is emitted when stock cannot be reserved for an order.
type InventoryReservationFailedEvent struct {
	OrderID    string
	ProductID  string
	Quantity   int
	Reason     string
	OccurredAt time.Time
}

func (InventoryReservationFailedEvent) EventName() string { return "inventory.reservation_failed" }

func NewInventoryReservationFailedEvent(orderID, productID string, quantity int, reason string) InventoryReservationFailedEvent {
	return InventoryReservationFailedEvent{
		OrderID:    orderID,
		ProductID:  productID,
		Quantity:   quantity,
		Reason:     reason,
		OccurredAt: time.Now().UTC(),
	}
}
