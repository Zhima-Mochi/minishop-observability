package main

import (
	"context"
	"errors"
	"log/slog"
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
)

func main() {
	orderRepo := memory.NewOrderRepository()
	inventoryRepo := memory.NewInventoryRepository()
	paymentService := appPayment.NewService(orderRepo, 0.7)
	idGenerator := id.NewUUIDGenerator()

	// Initialize structured logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{}))
	slog.SetDefault(logger)

	// In-memory event bus (acts as outbox/event publisher for demo)
	bus := outbox.NewBus(logger.With("component", "outbox"))
	bus.Start(context.Background())
	defer bus.Stop(context.Background())

	// Order service publishes events instead of mutating other contexts directly
	orderService := appOrder.NewService(orderRepo, idGenerator, bus)

	inventoryService := appInventory.NewService(inventoryRepo, bus)
	inventoryWorker := inventoryworker.New(bus, inventoryService)
	orderWorker := orderworker.New(orderRepo, bus, bus, logger.With("component", "order_worker"))
	paymentWorker := paymentworker.New(bus, paymentService)

	inventoryWorker.Start()
	orderWorker.Start()
	paymentWorker.Start()
	handler := httptransport.NewHandler(orderService, paymentService, logger.With("component", "http"))

	server := &http.Server{
		Addr:    ":8080",
		Handler: handler.Router(),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("http_server_start", "addr", server.Addr)
		err := server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http_server_error", "error", err)
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("http_server_shutdown_error", "error", err)
	} else {
		logger.Info("http_server_stopped")
	}
}
