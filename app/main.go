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
	"github.com/Zhima-Mochi/minishop-observability/app/internal/infrastructure/memory"
)

func main() {
	orderRepo := memory.NewOrderRepository()
	inventoryRepo := memory.NewInventoryRepository()
	inventoryService := appInventory.NewService(inventoryRepo, 100)
	paymentService := appPayment.NewService(0.7)
	idGenerator := id.NewUUIDGenerator()

	orderService := appOrder.NewService(orderRepo, inventoryService, paymentService, idGenerator)
	handler := httptransport.NewHandler(orderService, inventoryService)

	server := &http.Server{
		Addr:    ":8080",
		Handler: handler.Router(),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		err := server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			panic(err)
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		panic(err)
	}
}
