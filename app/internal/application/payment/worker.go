package payment

import (
	"context"

	"github.com/Zhima-Mochi/minishop-observability/app/internal/application"
	domorder "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/order"
	domoutbox "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/outbox"
	pstat "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/payment"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability/logctx"
)

const paymentWorker = "payment_worker"

type Worker struct {
	subscriber domoutbox.Subscriber
	useCase    application.UseCase[ProcessPaymentInput, *ProcessPaymentResult]
	tel        observability.Observability

	log          observability.Logger
	reqCounter   observability.Counter   // usecase_requests_total{use_case,outcome}
	durHistogram observability.Histogram // usecase_duration_seconds{use_case}
}

func New(
	subscriber domoutbox.Subscriber,
	useCase application.UseCase[ProcessPaymentInput, *ProcessPaymentResult],
	tel observability.Observability,
) *Worker {
	baseLog := observability.NopLogger()
	metricsProvider := observability.NopMetrics()
	if tel != nil {
		baseLog = tel.Logger()
		metricsProvider = tel.Metrics()
	}

	return &Worker{
		subscriber:   subscriber,
		useCase:      useCase,
		tel:          tel,
		log:          baseLog,
		reqCounter:   metricsProvider.Counter(observability.MUsecaseRequests),
		durHistogram: metricsProvider.Histogram(observability.MUsecaseDuration),
	}
}

func (w *Worker) Start() {
	if w.subscriber == nil || w.useCase == nil {
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

	res, err := w.useCase.Execute(ctx, ProcessPaymentInput{OrderID: evt.OrderID, Amount: 0})
	if err != nil {
		logger.Warn("payment_processing_failed",
			observability.F("order_id", evt.OrderID),
			observability.F("error", err.Error()),
		)
		return err
	}

	status := pstat.StatusFailed
	if res != nil {
		status = res.Status
	}

	logger.Info("payment_processed",
		observability.F("order_id", evt.OrderID),
		observability.F("status", string(status)),
	)
	return nil
}
