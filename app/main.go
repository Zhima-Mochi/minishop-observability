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
	obsprovider "github.com/Zhima-Mochi/minishop-observability/app/internal/infrastructure/observability"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/infrastructure/observability/oteltrace"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/infrastructure/observability/prometrics"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/infrastructure/observability/zaplogger"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/infrastructure/outbox"
	coreobservability "github.com/Zhima-Mochi/minishop-observability/app/internal/observability"
	httppresentation "github.com/Zhima-Mochi/minishop-observability/app/internal/presentation/http"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	serviceName := getenvDefault("SERVICE_NAME", "minishop")
	env := getenvDefault("ENV", "dev")

	baseLogger := zaplogger.New(
		coreobservability.F("service", serviceName),
		coreobservability.F("env", env),
	)
	if syncer, ok := baseLogger.(interface{ Sync() error }); ok {
		defer func() { _ = syncer.Sync() }()
	}

	metrics := prometrics.New(serviceName, "app")
	usecaseRequests := metrics.Counter(
		string(coreobservability.MUsecaseRequests),
		"Total number of use case invocations.",
		"use_case", "outcome",
	)
	usecaseDurations := metrics.Histogram(
		string(coreobservability.MUsecaseDuration),
		"Duration of use case execution in seconds.",
		prometheus.DefBuckets,
		"use_case",
	)
	httpRequests := metrics.Counter(
		string(coreobservability.MHTTPRequests),
		"Total number of HTTP requests.",
		"method", "route", "status",
	)
	httpDurations := metrics.Histogram(
		string(coreobservability.MHTTPRequestDuration),
		"Duration of HTTP request handling in seconds.",
		prometheus.DefBuckets,
		"method", "route", "status",
	)
	externalRequests := metrics.Counter(
		string(coreobservability.MExternalRequests),
		"Total number of outbound requests made by the service.",
		"peer", "endpoint", "outcome",
	)
	externalDurations := metrics.Histogram(
		string(coreobservability.MExternalRequestDuration),
		"Duration of outbound requests in seconds.",
		prometheus.DefBuckets,
		"peer", "endpoint",
	)

	tel := obsprovider.New(
		oteltrace.New(serviceName),
		baseLogger,
		map[coreobservability.MetricKey]coreobservability.Counter{
			coreobservability.MUsecaseRequests:  usecaseRequests,
			coreobservability.MHTTPRequests:     httpRequests,
			coreobservability.MExternalRequests: externalRequests,
		},
		map[coreobservability.MetricKey]coreobservability.Histogram{
			coreobservability.MUsecaseDuration:         usecaseDurations,
			coreobservability.MHTTPRequestDuration:     httpDurations,
			coreobservability.MExternalRequestDuration: externalDurations,
		},
	)

	orderRepo := memory.NewOrderRepository()
	inventoryRepo := memory.NewInventoryRepository()
	idGenerator := id.NewUUIDGenerator()

	// In-memory event bus (acts as outbox/event publisher for demo)
	bus := outbox.NewBus(baseLogger, tel)
	bus.Start(context.Background())
	defer bus.Stop(context.Background())

	// Order use case publishes events instead of mutating other contexts directly
	orderUseCase := appOrder.NewCreateOrderUseCase(orderRepo, idGenerator, bus, tel)
	paymentUseCase := appPayment.NewProcessPaymentUseCase(orderRepo, tel)

	inventoryUseCase := appInventory.NewReserveInventoryUseCase(inventoryRepo, bus, tel)
	inventoryWorker := appInventory.New(bus, inventoryUseCase, tel, baseLogger)
	orderWorker := appOrder.New(orderRepo, bus, bus, tel, baseLogger)
	paymentWorker := appPayment.New(bus, paymentUseCase, tel)

	inventoryWorker.Start()
	orderWorker.Start()
	paymentWorker.Start()
	handler := httppresentation.NewHandler(orderUseCase, paymentUseCase, baseLogger, tel)
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.Handle("/", handler.Router())

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	systemLogger := tel.Logger().With(
		coreobservability.F("component", "system"),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		systemLogger.Info("http_server_start",
			coreobservability.F("addr", server.Addr),
		)
		err := server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			systemLogger.Error("http_server_error",
				coreobservability.F("error", err),
			)
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		systemLogger.Error("http_server_shutdown_error",
			coreobservability.F("error", err),
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
