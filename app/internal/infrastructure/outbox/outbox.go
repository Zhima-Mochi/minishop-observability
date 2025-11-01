package outbox

import (
	"context"
	"runtime/debug"
	"sync"
	"time"

	domoutbox "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/outbox"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability/logctx"
)

// Bus is an in-memory event bus suitable for demo/testing and simple outbox-like fanout.
// It is not durable; for production use, persist events (true Outbox pattern) and dispatch from a worker.
type Bus struct {
	mu          sync.RWMutex
	subs        map[string][]domoutbox.Handler
	queue       chan domoutbox.Event
	startOnce   sync.Once
	stopOnce    sync.Once
	cancel      context.CancelFunc
	concurrency int
	log         observability.Logger
	tel         observability.Observability
}

// NewBus creates a bus with a buffered queue and a concurrency cap.
const componentOutbox = "outbox"

func NewBus(logger observability.Logger, tel observability.Observability) *Bus {
	return &Bus{
		subs:        make(map[string][]domoutbox.Handler),
		queue:       make(chan domoutbox.Event, 1024), // buffer for backpressure
		concurrency: 8,                                // per-event handler fanout cap
		log:         logger.With(observability.F("component", componentOutbox)),
		tel:         tel,
	}
}

func (b *Bus) Subscribe(eventName string, h domoutbox.Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[eventName] = append(b.subs[eventName], h)
}

func (b *Bus) Start(ctx context.Context) {
	b.startOnce.Do(func() {
		bg, cancel := context.WithCancel(ctx)
		b.cancel = cancel
		go b.dispatchLoop(bg)
		logger := logctx.FromOr(ctx, b.log)
		logger.Info("event_bus_started")
	})
}

func (b *Bus) Stop(ctx context.Context) {
	b.stopOnce.Do(func() {
		if b.cancel != nil {
			b.cancel()
		}

		close(b.queue)
		logger := logctx.FromOr(ctx, b.log)
		logger.Info("event_bus_stopped")
	})
}

func (b *Bus) Publish(ctx context.Context, e domoutbox.Event) error {
	if e == nil {
		return nil
	}
	select {
	case b.queue <- e:
		logger := logctx.FromOr(ctx, b.log).With(observability.F("event", e.EventName()))
		logger.Debug("event_enqueued")
		return nil
	case <-ctx.Done():
		logger := logctx.FromOr(ctx, b.log).With(observability.F("event", e.EventName()))
		logger.Warn("event_enqueue_aborted",
			observability.F("error", ctx.Err()),
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

func (b *Bus) fanout(ctx context.Context, e domoutbox.Event) {
	name := e.EventName()

	b.mu.RLock()
	handlers := append([]domoutbox.Handler(nil), b.subs[name]...)
	b.mu.RUnlock()

	if len(handlers) == 0 {
		logger := logctx.FromOr(ctx, b.log).With(observability.F("event", name))
		logger.Debug("event_dropped_no_subscriber")
		return
	}

	ctx = context.WithoutCancel(ctx)
	baseLogger := b.log
	ctx = logctx.With(ctx, baseLogger)

	sem := make(chan struct{}, b.concurrency)
	var wg sync.WaitGroup

	for _, h := range handlers {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					logger := logctx.FromOr(ctx, b.log).With(observability.F("event", name))
					logger.Error("event_handler_panic",
						observability.F("event", name),
						observability.F("panic", r),
						observability.F("stack", string(debug.Stack())),
					)
				}
				<-sem
				wg.Done()
			}()

			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			ctx = logctx.With(ctx, baseLogger.With(observability.F("event", name)))
			err := h(ctx, e)
			cancel()
			if err != nil {
				baseLogger.Warn("event_handler_error",
					observability.F("error", err),
				)
			}
		}()
	}

	wg.Wait()

	baseLogger.Debug("event_fanned_out",
		observability.F("event", name),
		observability.F("handlers", len(handlers)),
	)
}
