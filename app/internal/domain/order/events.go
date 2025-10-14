package order

import "time"

// OrderCreatedEvent is a domain event emitted when a new order is created.
// It is intended to be handled by other bounded contexts (e.g., Inventory).
type OrderCreatedEvent struct {
	OrderID    string
	CustomerID string
	ProductID  string
	Quantity   int
	Amount     int64
	OccurredAt time.Time
}

func (OrderCreatedEvent) EventName() string { return "order.created" }

func NewOrderCreatedEvent(o *Order) OrderCreatedEvent {
	return OrderCreatedEvent{
		OrderID:    o.ID,
		CustomerID: o.CustomerID,
		ProductID:  o.ProductID,
		Quantity:   o.Quantity,
		Amount:     o.Amount,
		OccurredAt: time.Now().UTC(),
	}
}

// OrderInventoryReservedEvent is emitted when inventory reservation succeeds for an order.
type OrderInventoryReservedEvent struct {
	OrderID    string
	OccurredAt time.Time
}

func (OrderInventoryReservedEvent) EventName() string { return "order.inventory_reserved" }

func NewOrderInventoryReservedEvent(o *Order) OrderInventoryReservedEvent {
	return OrderInventoryReservedEvent{
		OrderID:    o.ID,
		OccurredAt: time.Now().UTC(),
	}
}

// OrderInventoryReservationFailedEvent is emitted when inventory reservation fails.
type OrderInventoryReservationFailedEvent struct {
	OrderID    string
	Reason     string
	OccurredAt time.Time
}

func (OrderInventoryReservationFailedEvent) EventName() string { return "order.inventory_failed" }

func NewOrderInventoryReservationFailedEvent(o *Order, reason string) OrderInventoryReservationFailedEvent {
	return OrderInventoryReservationFailedEvent{
		OrderID:    o.ID,
		Reason:     reason,
		OccurredAt: time.Now().UTC(),
	}
}
