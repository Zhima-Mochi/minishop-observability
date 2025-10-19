package outbox

import (
	"context"
	"runtime/debug"
	"sync"
	"time"

	"github.com/Zhima-Mochi/minishop-observability/app/internal/pkg/logging"
	"go.uber.org/zap"
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
	startOnce   sync.Once
	stopOnce    sync.Once
	cancel      context.CancelFunc
	concurrency int
}

// NewBus creates a bus with a buffered queue and a concurrency cap.
func NewBus() *Bus {
	return &Bus{
		subs:        make(map[string][]Handler),
		queue:       make(chan Event, 1024), // buffer for backpressure
		concurrency: 8,                      // per-event handler fanout cap
	}
}

func (b *Bus) Subscribe(eventName string, h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[eventName] = append(b.subs[eventName], h)
}

func (b *Bus) Start(ctx context.Context) {
	b.startOnce.Do(func() {
		bg, cancel := context.WithCancel(ctx)
		b.cancel = cancel
		go b.dispatchLoop(bg)
		logger := logging.FromContext(ctx).With(zap.String("component", "outbox"))
		logger.Info("event_bus_started")
	})
}

func (b *Bus) Stop(ctx context.Context) {
	b.stopOnce.Do(func() {
		if b.cancel != nil {
			b.cancel()
		}

		close(b.queue)
		logger := logging.FromContext(ctx).With(zap.String("component", "outbox"))
		logger.Info("event_bus_stopped")
	})
}

func (b *Bus) Publish(ctx context.Context, e Event) error {
	if e == nil {
		return nil
	}
	select {
	case b.queue <- e:
		logger := logging.FromContext(ctx).With(zap.String("component", "outbox"))
		logger.Debug("event_enqueued", zap.String("event", e.EventName()))
		return nil
	case <-ctx.Done():
		logger := logging.FromContext(ctx).With(zap.String("component", "outbox"))
		logger.Warn("event_enqueue_aborted",
			zap.String("event", e.EventName()),
			zap.Error(ctx.Err()),
		)
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
		logger := logging.FromContext(ctx).With(zap.String("component", "outbox"))
		logger.Debug("event_dropped_no_subscriber", zap.String("event", name))
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
				if r := recover(); r != nil {
					logger := logging.FromContext(ctx).With(zap.String("component", "outbox"))
					logger.Error("event_handler_panic",
						zap.String("event", name),
						zap.Any("panic", r),
						zap.String("stack", string(debug.Stack())),
					)
				}
				<-sem
				wg.Done()
			}()

			hctx, cancel := context.WithTimeout(bg, 30*time.Second)
			err := h(hctx, e)
			cancel()
			if err != nil {
				logger := logging.FromContext(ctx).With(zap.String("component", "outbox"))
				logger.Warn("event_handler_error",
					zap.String("event", name),
					zap.Error(err),
				)
			}
		}()
	}

	wg.Wait()

	logger := logging.FromContext(ctx).With(zap.String("component", "outbox"))
	logger.Debug("event_fanned_out",
		zap.String("event", name),
		zap.Int("handlers", len(handlers)),
	)
}
