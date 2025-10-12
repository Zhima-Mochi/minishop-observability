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
	"github.com/Zhima-Mochi/minishop-observability/app/internal/infrastructure/memory"
)

func main() {
	orderRepo := memory.NewOrderRepository()
	inventoryRepo := memory.NewInventoryRepository()
	inventoryService := appInventory.NewService(inventoryRepo, 100)
	paymentService := appPayment.NewService(0.7)
	idGenerator := id.NewUUIDGenerator()

	// Initialize structured logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{}))
	slog.SetDefault(logger)

	orderService := appOrder.NewService(orderRepo, inventoryService, paymentService, idGenerator)
	handler := httptransport.NewHandler(orderService, inventoryService, logger.With("component", "http"))

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
