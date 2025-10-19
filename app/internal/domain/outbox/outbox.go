package outbox

import "context"

// Event is any domain event with a name identifier.
type Event interface {
	EventName() string
}

// Handler processes a published event.
type Handler func(ctx context.Context, e Event) error

// Publisher publishes events to interested subscribers.
type Publisher interface {
	Publish(ctx context.Context, e Event) error
}

// Subscriber registers handlers for event names.
type Subscriber interface {
	Subscribe(eventName string, h Handler)
}
