package order

// OrderState implements the state pattern for order lifecycle transitions.
type OrderState interface {
	Status() Status
	OnInventoryReserved(o *Order) (OrderState, error)
	OnInventoryFailed(o *Order, reason string) (OrderState, error)
	OnPaymentSucceeded(o *Order) (OrderState, error)
	OnPaymentFailed(o *Order, reason string) (OrderState, error)
}

type pendingState struct{}

func (pendingState) Status() Status { return StatusPending }

func (pendingState) OnInventoryReserved(o *Order) (OrderState, error) {
	o.FailureReason = ""
	return inventoryReservedState{}, nil
}

func (pendingState) OnInventoryFailed(o *Order, reason string) (OrderState, error) {
	o.FailureReason = reason
	return inventoryFailedState{}, nil
}

func (pendingState) OnPaymentSucceeded(*Order) (OrderState, error) {
	return nil, ErrInvalidStateTransition
}

func (pendingState) OnPaymentFailed(*Order, string) (OrderState, error) {
	return nil, ErrInvalidStateTransition
}

type inventoryReservedState struct{}

func (inventoryReservedState) Status() Status { return StatusInventoryReserved }

func (inventoryReservedState) OnInventoryReserved(o *Order) (OrderState, error) {
	o.FailureReason = ""
	return inventoryReservedState{}, nil
}

func (inventoryReservedState) OnInventoryFailed(*Order, string) (OrderState, error) {
	return nil, ErrInvalidStateTransition
}

func (inventoryReservedState) OnPaymentSucceeded(o *Order) (OrderState, error) {
	o.FailureReason = ""
	return completedState{}, nil
}

func (inventoryReservedState) OnPaymentFailed(o *Order, reason string) (OrderState, error) {
	o.FailureReason = reason
	return paymentFailedState{}, nil
}

type inventoryFailedState struct{}

func (inventoryFailedState) Status() Status { return StatusInventoryFailed }

func (inventoryFailedState) OnInventoryReserved(*Order) (OrderState, error) {
	return nil, ErrInvalidStateTransition
}

func (inventoryFailedState) OnInventoryFailed(o *Order, reason string) (OrderState, error) {
	o.FailureReason = reason
	return inventoryFailedState{}, nil
}

func (inventoryFailedState) OnPaymentSucceeded(*Order) (OrderState, error) {
	return nil, ErrInvalidStateTransition
}

func (inventoryFailedState) OnPaymentFailed(*Order, string) (OrderState, error) {
	return nil, ErrInvalidStateTransition
}

type completedState struct{}

func (completedState) Status() Status { return StatusCompleted }

func (completedState) OnInventoryReserved(*Order) (OrderState, error) {
	return completedState{}, nil
}

func (completedState) OnInventoryFailed(*Order, string) (OrderState, error) {
	return nil, ErrInvalidStateTransition
}

func (completedState) OnPaymentSucceeded(*Order) (OrderState, error) {
	return completedState{}, nil
}

func (completedState) OnPaymentFailed(*Order, string) (OrderState, error) {
	return nil, ErrInvalidStateTransition
}

type paymentFailedState struct{}

func (paymentFailedState) Status() Status { return StatusPaymentFailed }

func (paymentFailedState) OnInventoryReserved(*Order) (OrderState, error) {
	return nil, ErrInvalidStateTransition
}

func (paymentFailedState) OnInventoryFailed(*Order, string) (OrderState, error) {
	return nil, ErrInvalidStateTransition
}

func (paymentFailedState) OnPaymentSucceeded(o *Order) (OrderState, error) {
	o.FailureReason = ""
	return completedState{}, nil
}

func (paymentFailedState) OnPaymentFailed(o *Order, reason string) (OrderState, error) {
	o.FailureReason = reason
	return paymentFailedState{}, nil
}
