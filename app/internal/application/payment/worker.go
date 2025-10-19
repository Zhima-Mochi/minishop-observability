package payment

import (
	"context"

	domorder "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/order"
	domoutbox "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/outbox"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability/logctx"
)

const paymentWorker = "payment_worker"

type Worker struct {
	subscriber domoutbox.Subscriber
	service    *Service
	tel        observability.Telemetry

	log          observability.Logger
	reqCounter   observability.Counter   // usecase_requests_total{use_case,outcome}
	durHistogram observability.Histogram // usecase_duration_seconds{use_case}
}

func New(
	subscriber domoutbox.Subscriber,
	service *Service,
	tel observability.Telemetry,
) *Worker {
	return &Worker{
		subscriber:   subscriber,
		service:      service,
		tel:          tel,
		log:          tel.Logger(),
		reqCounter:   tel.Counter("usecase_requests_total"),
		durHistogram: tel.Histogram("usecase_duration_seconds"),
	}
}

func (w *Worker) Start() {
	if w.subscriber == nil || w.service == nil {
		return
	}
	w.subscriber.Subscribe(domorder.OrderInventoryReservedEvent{}.EventName(), w.handleOrderInventoryReserved)
}

func (w *Worker) handleOrderInventoryReserved(ctx context.Context, e domoutbox.Event) error {
	logger := logctx.FromOr(ctx, w.log)
	logger = logger.With(
		observability.F("event", e.EventName()),
	)

	evt, ok := e.(domorder.OrderInventoryReservedEvent)
	if !ok {
		return nil
	}

	status, err := w.service.ProcessPayment(ctx, evt.OrderID, 0)
	if err != nil {
		logger.Warn("payment_processing_failed",
			observability.F("order_id", evt.OrderID),
			observability.F("error", err.Error()),
		)
		return err
	}

	logger.Info("payment_processed",
		observability.F("order_id", evt.OrderID),
		observability.F("status", string(status)),
	)
	return nil
}
