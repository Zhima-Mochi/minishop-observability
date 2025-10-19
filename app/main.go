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
	"github.com/Zhima-Mochi/minishop-observability/app/internal/infrastructure/id"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/infrastructure/memory"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/infrastructure/observability/oteltrace"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/infrastructure/observability/prometrics"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/infrastructure/observability/telemetry"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/infrastructure/observability/zaplogger"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/infrastructure/outbox"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability"
	httppresentation "github.com/Zhima-Mochi/minishop-observability/app/internal/presentation/http"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	serviceName := getenvDefault("SERVICE_NAME", "minishop")
	env := getenvDefault("ENV", "dev")

	baseLogger := zaplogger.New(
		observability.F("service", serviceName),
		observability.F("env", env),
	)
	if syncer, ok := baseLogger.(interface{ Sync() error }); ok {
		defer func() { _ = syncer.Sync() }()
	}

	metrics := prometrics.New(serviceName, "app")
	usecaseRequests := metrics.Counter(
		"usecase_requests_total",
		"Total number of use case invocations.",
		"use_case", "outcome",
	)
	usecaseDurations := metrics.Histogram(
		"usecase_duration_seconds",
		"Duration of use case execution in seconds.",
		prometheus.DefBuckets,
		"use_case",
	)
	httpRequests := metrics.Counter(
		"http_requests_total",
		"Total number of HTTP requests.",
		"method", "route", "status",
	)
	httpDurations := metrics.Histogram(
		"http_request_duration_seconds",
		"Duration of HTTP request handling in seconds.",
		prometheus.DefBuckets,
		"method", "route", "status",
	)

	tel := telemetry.New(
		oteltrace.New(serviceName),
		baseLogger,
		map[string]observability.Counter{
			"usecase_requests_total": usecaseRequests,
			"http_requests_total":    httpRequests,
		},
		map[string]observability.Histogram{
			"usecase_duration_seconds":      usecaseDurations,
			"http_request_duration_seconds": httpDurations,
		},
	)

	orderRepo := memory.NewOrderRepository()
	inventoryRepo := memory.NewInventoryRepository()
	idGenerator := id.NewUUIDGenerator()

	// In-memory event bus (acts as outbox/event publisher for demo)
	bus := outbox.NewBus(baseLogger, tel)
	bus.Start(context.Background())
	defer bus.Stop(context.Background())

	// Order service publishes events instead of mutating other contexts directly
	orderService := appOrder.NewService(orderRepo, idGenerator, bus, tel)
	paymentService := appPayment.NewService(orderRepo, tel)

	inventoryService := appInventory.NewService(inventoryRepo, bus, tel)
	inventoryWorker := appInventory.New(bus, inventoryService, tel, baseLogger)
	orderWorker := appOrder.New(orderRepo, bus, bus, tel, baseLogger)
	paymentWorker := appPayment.New(bus, paymentService, tel)

	inventoryWorker.Start()
	orderWorker.Start()
	paymentWorker.Start()
	handler := httppresentation.NewHandler(orderService, paymentService, baseLogger, tel)
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.Handle("/", handler.Router())

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	systemLogger := tel.Logger().With(
		observability.F("component", "system"),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		systemLogger.Info("http_server_start",
			observability.F("addr", server.Addr),
		)
		err := server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			systemLogger.Error("http_server_error",
				observability.F("error", err),
			)
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		systemLogger.Error("http_server_shutdown_error",
			observability.F("error", err),
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
