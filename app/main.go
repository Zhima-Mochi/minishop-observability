package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	appInventory "github.com/Zhima-Mochi/minishop-observability/app/internal/application/inventory"
	appOrder "github.com/Zhima-Mochi/minishop-observability/app/internal/application/order"
	appPayment "github.com/Zhima-Mochi/minishop-observability/app/internal/application/payment"
	httptransport "github.com/Zhima-Mochi/minishop-observability/app/internal/infrastructure/http"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/infrastructure/id"
	inventoryworker "github.com/Zhima-Mochi/minishop-observability/app/internal/infrastructure/inventory/worker"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/infrastructure/memory"
	orderworker "github.com/Zhima-Mochi/minishop-observability/app/internal/infrastructure/order/worker"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/infrastructure/outbox"
	paymentworker "github.com/Zhima-Mochi/minishop-observability/app/internal/infrastructure/payment/worker"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/pkg/logging"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

func main() {
	orderRepo := memory.NewOrderRepository()
	inventoryRepo := memory.NewInventoryRepository()
	paymentService := appPayment.NewService(orderRepo)
	idGenerator := id.NewUUIDGenerator()

	serviceName := getenvDefault("SERVICE_NAME", "minishop")
	env := getenvDefault("ENV", "dev")
	baseLogger := logging.MustNewLogger(serviceName, env)
	defer func() { _ = baseLogger.Sync() }()
	zap.ReplaceGlobals(baseLogger)

	systemLogger := logging.WithTrace(baseLogger, logging.SystemTraceID, logging.SystemSpanID)

	usecaseRequests := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "usecase_requests_total",
			Help: "Total number of use case invocations.",
		},
		[]string{"use_case", "outcome"},
	)
	usecaseDurations := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "usecase_duration_seconds",
			Help:    "Duration of use case execution in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"use_case"},
	)
	orderEventPublishFailures := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "order_event_publish_failed_total",
			Help: "Count of order-related event publish failures.",
		},
		[]string{"event"},
	)
	prometheus.MustRegister(usecaseRequests, usecaseDurations, orderEventPublishFailures)

	// In-memory event bus (acts as outbox/event publisher for demo)
	bus := outbox.NewBus()
	bus.Start(context.Background())
	defer bus.Stop(context.Background())

	// Order service publishes events instead of mutating other contexts directly
	orderService := appOrder.NewService(orderRepo, idGenerator, bus)

	inventoryService := appInventory.NewService(inventoryRepo, bus)
	inventoryWorker := inventoryworker.New(bus, inventoryService)
	orderWorker := orderworker.New(orderRepo, bus, bus)
	paymentWorker := paymentworker.New(bus, paymentService)

	inventoryWorker.Start()
	orderWorker.Start()
	paymentWorker.Start()
	handler := httptransport.NewHandler(orderService, paymentService)
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.Handle("/", handler.Router())

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		systemLogger.Info("http_server_start",
			zap.String("addr", server.Addr),
		)
		err := server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			systemLogger.Error("http_server_error",
				zap.Error(err),
			)
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		systemLogger.Error("http_server_shutdown_error",
			zap.Error(err),
		)
	} else {
		systemLogger.Info("http_server_stopped")
	}
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
