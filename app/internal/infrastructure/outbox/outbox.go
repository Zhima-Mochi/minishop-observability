package outbox

import (
	"context"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"
)

// Event is any domain event with a name identifier.
type Event interface{ EventName() string }

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

// Bus is an in-memory event bus suitable for demo/testing and simple outbox-like fanout.
// It is not durable; for production use, persist events (true Outbox pattern) and dispatch from a worker.
type Bus struct {
	mu          sync.RWMutex
	subs        map[string][]Handler
	queue       chan Event
	log         *slog.Logger
	startOnce   sync.Once
	stopOnce    sync.Once
	cancel      context.CancelFunc
	concurrency int
}

// NewBus creates a bus with a buffered queue and a concurrency cap.
func NewBus(logger *slog.Logger) *Bus {
	return &Bus{
		subs:        make(map[string][]Handler),
		queue:       make(chan Event, 1024), // buffer for backpressure
		log:         logger,
		concurrency: 8, // per-event handler fanout cap
	}
}

func (b *Bus) Subscribe(eventName string, h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[eventName] = append(b.subs[eventName], h)
}

func (b *Bus) Start(ctx context.Context) {
	b.startOnce.Do(func() {
		bg, cancel := context.WithCancel(context.Background())
		b.cancel = cancel
		go b.dispatchLoop(bg)
		if b.log != nil {
			b.log.Info("event_bus_started")
		}
	})
}

func (b *Bus) Stop(ctx context.Context) {
	b.stopOnce.Do(func() {
		if b.cancel != nil {
			b.cancel()
		}

		close(b.queue)
		if b.log != nil {
			b.log.Info("event_bus_stopped")
		}
	})
}

func (b *Bus) Publish(ctx context.Context, e Event) error {
	if e == nil {
		return nil
	}
	select {
	case b.queue <- e:
		if b.log != nil {
			b.log.Debug("event_enqueued", "event", e.EventName())
		}
		return nil
	case <-ctx.Done():
		if b.log != nil {
			b.log.Warn("event_enqueue_aborted", "event", e.EventName(), "reason", ctx.Err())
		}
		return ctx.Err()
	}
}

func (b *Bus) dispatchLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-b.queue:
			if !ok {
				return
			}
			b.fanout(ctx, e)
		}
	}
}

func (b *Bus) fanout(ctx context.Context, e Event) {
	name := e.EventName()

	b.mu.RLock()
	handlers := append([]Handler(nil), b.subs[name]...)
	b.mu.RUnlock()

	if len(handlers) == 0 {
		if b.log != nil {
			b.log.Debug("event_dropped_no_subscriber", "event", name)
		}
		return
	}

	bg := context.WithoutCancel(ctx)

	sem := make(chan struct{}, b.concurrency)
	var wg sync.WaitGroup

	for _, h := range handlers {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() {
				if r := recover(); r != nil && b.log != nil {
					b.log.Error("event_handler_panic", "event", name, "panic", r, "stack", string(debug.Stack()))
				}
				<-sem
				wg.Done()
			}()

			hctx, cancel := context.WithTimeout(bg, 30*time.Second)
			err := h(hctx, e)
			cancel()
			if err != nil && b.log != nil {
				b.log.Warn("event_handler_error", "event", name, "error", err)
			}
		}()
	}

	wg.Wait()

	if b.log != nil {
		b.log.Debug("event_fanned_out", "event", name, "handlers", len(handlers))
	}
}
