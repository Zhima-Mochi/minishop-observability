package httptransport

import (
    "context"
    "encoding/json"
    "errors"
    "log/slog"
    "net/http"
    "time"

    appOrder "github.com/Zhima-Mochi/minishop-observability/app/internal/application/order"
    appPayment "github.com/Zhima-Mochi/minishop-observability/app/internal/application/payment"
    domainInventory "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/inventory"
    domainOrder "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/order"
    domainPayment "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/payment"
    "github.com/Zhima-Mochi/minishop-observability/app/internal/pkg/logging"
    "github.com/google/uuid"
)

type Handler struct {
    orderService     *appOrder.Service
    paymentService   *appPayment.Service
    logger           *slog.Logger
}

func NewHandler(orderSvc *appOrder.Service, paymentSvc *appPayment.Service, logger *slog.Logger) *Handler {
    return &Handler{
        orderService:     orderSvc,
        paymentService:   paymentSvc,
        logger:           logger,
    }
}

func (h *Handler) Router() http.Handler {
	mux := http.NewServeMux()

    mux.HandleFunc("/order", h.method(http.MethodPost, h.handleCreateOrder))
    mux.HandleFunc("/payment/pay", h.method(http.MethodPost, h.handleProcessPayment))
    mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodGet {
            writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
            return
        }
        w.WriteHeader(http.StatusOK)
        _, _ = w.Write([]byte("ok"))
    })

	return h.withLogging(mux)
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
	logger := logging.FromContext(r.Context())
	var req createOrderRequest
	if err := decodeJSON(r.Context(), r, &req); err != nil {
		logger.Warn("bad_request", "path", r.URL.Path, "error", err)
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
		logger.Error("create_order_failed", "error", err)
		writeDomainError(w, err)
		return
	}

    writeJSON(w, http.StatusCreated, createOrderResponse{
        OrderID: result.OrderID,
        Status:  result.Status,
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
    logger := logging.FromContext(r.Context())
    var req processPaymentRequest
    if err := decodeJSON(r.Context(), r, &req); err != nil {
        logger.Warn("bad_request", "path", r.URL.Path, "error", err)
        writeError(w, http.StatusBadRequest, err)
        return
    }

    status, err := h.paymentService.ProcessPayment(r.Context(), req.OrderID, req.Amount)
    if err != nil {
        logger.Error("payment_failed", "error", err, "order_id", req.OrderID)
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

// withLogging adds a simple access log around the handler.
func (h *Handler) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lrw := &loggingResponseWriter{ResponseWriter: w, status: http.StatusOK}
		reqID := uuid.NewString()
		reqLogger := h.logger.With("request_id", reqID)
		ctx := logging.ContextWithLogger(r.Context(), reqLogger)
		next.ServeHTTP(lrw, r.WithContext(ctx))
		reqLogger.Info("http_request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", lrw.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

type loggingResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *loggingResponseWriter) WriteHeader(statusCode int) {
	w.status = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
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
