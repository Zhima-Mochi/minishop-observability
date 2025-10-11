package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	appInventory "github.com/Zhima-Mochi/minishop-observability/app/internal/application/inventory"
	appOrder "github.com/Zhima-Mochi/minishop-observability/app/internal/application/order"
	domainInventory "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/inventory"
	domainOrder "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/order"
	domainPayment "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/payment"
)

type Handler struct {
	orderService     *appOrder.Service
	inventoryService *appInventory.Service
}

func NewHandler(orderSvc *appOrder.Service, inventorySvc *appInventory.Service) *Handler {
	return &Handler{
		orderService:     orderSvc,
		inventoryService: inventorySvc,
	}
}

func (h *Handler) Router() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/order", h.method(http.MethodPost, h.handleCreateOrder))
	mux.HandleFunc("/inventory/deduct", h.method(http.MethodPost, h.handleDeductInventory))
	mux.HandleFunc("/payment/pay", h.method(http.MethodPost, h.handleProcessPayment))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	return mux
}

type createOrderRequest struct {
	CustomerID string `json:"customer_id"`
	ProductID  string `json:"product_id"`
	Quantity   int    `json:"quantity"`
	Amount     int64  `json:"amount"`
}

type createOrderResponse struct {
	OrderID string             `json:"order_id"`
	Status  domainOrder.Status `json:"status"`
}

func (h *Handler) handleCreateOrder(w http.ResponseWriter, r *http.Request) {
	var req createOrderRequest
	if err := decodeJSON(r.Context(), r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	result, err := h.orderService.CreateOrder(r.Context(), appOrder.CreateOrderInput{
		CustomerID: req.CustomerID,
		ProductID:  req.ProductID,
		Quantity:   req.Quantity,
		Amount:     req.Amount,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, createOrderResponse{
		OrderID: result.OrderID,
		Status:  result.Status,
	})
}

type deductInventoryRequest struct {
	ProductID string `json:"product_id"`
	Quantity  int    `json:"quantity"`
}

type deductInventoryResponse struct {
	ProductID string `json:"product_id"`
	Remaining int    `json:"remaining"`
}

func (h *Handler) handleDeductInventory(w http.ResponseWriter, r *http.Request) {
	var req deductInventoryRequest
	if err := decodeJSON(r.Context(), r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	remaining, err := h.inventoryService.Deduct(r.Context(), req.ProductID, req.Quantity)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, deductInventoryResponse{
		ProductID: req.ProductID,
		Remaining: remaining,
	})
}

type processPaymentRequest struct {
	OrderID string `json:"order_id"`
	Amount  int64  `json:"amount"`
}

type processPaymentResponse struct {
	OrderID string               `json:"order_id"`
	Status  domainPayment.Status `json:"status"`
}

func (h *Handler) handleProcessPayment(w http.ResponseWriter, r *http.Request) {
	var req processPaymentRequest
	if err := decodeJSON(r.Context(), r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	status, err := h.orderService.ProcessPayment(r.Context(), req.OrderID, req.Amount)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, processPaymentResponse{
		OrderID: req.OrderID,
		Status:  status,
	})
}

func (h *Handler) method(method string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
			return
		}
		handler(w, r)
	}
}

func decodeJSON(ctx context.Context, r *http.Request, dst any) error {
	_ = ctx
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domainOrder.ErrNotFound),
		errors.Is(err, domainInventory.ErrNotFound):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, domainInventory.ErrInvalidQuantity),
		errors.Is(err, domainInventory.ErrInsufficientStock),
		errors.Is(err, domainOrder.ErrInvalidAmount),
		errors.Is(err, domainOrder.ErrInvalidQuantity):
		writeError(w, http.StatusBadRequest, err)
	default:
		writeError(w, http.StatusInternalServerError, err)
	}
}
