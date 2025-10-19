package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	appOrder "github.com/Zhima-Mochi/minishop-observability/app/internal/application/order"
	appPayment "github.com/Zhima-Mochi/minishop-observability/app/internal/application/payment"
	domainInventory "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/inventory"
	domainOrder "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/order"
	domainPayment "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/payment"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/pkg/logging"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

type Handler struct {
	orderService   *appOrder.Service
	paymentService *appPayment.Service
}

func NewHandler(orderSvc *appOrder.Service, paymentSvc *appPayment.Service) *Handler {
	return &Handler{
		orderService:   orderSvc,
		paymentService: paymentSvc,
	}
}

func muxHandle(mux *http.ServeMux, method, route string, handler http.HandlerFunc) {
	mux.HandleFunc(route, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		ctx := contextWithRoute(r.Context(), route)
		r = r.WithContext(ctx)

		withMetrics(withTrace(withLogging(handler))).ServeHTTP(w, r)
	})
}

func (h *Handler) Router() http.Handler {
	mux := http.NewServeMux()

	muxHandle(mux, http.MethodPost, "/order", h.handleCreateOrder)
	muxHandle(mux, http.MethodPost, "/payment/pay", h.handleProcessPayment)
	muxHandle(mux, http.MethodGet, "/health", h.handleHealth)

	return mux
}

type createOrderRequest struct {
	CustomerID     string `json:"customer_id"`
	IdempotencyKey string `json:"idempotency_key"`
	ProductID      string `json:"product_id"`
	Quantity       int    `json:"quantity"`
	Amount         int64  `json:"amount"`
}

type createOrderResponse struct {
	OrderID string             `json:"order_id"`
	Status  domainOrder.Status `json:"status"`
}

func (h *Handler) handleCreateOrder(w http.ResponseWriter, r *http.Request) {
	logger := logging.FromContext(r.Context())
	route := routeFromContext(r.Context())
	var req createOrderRequest
	if err := decodeJSON(r.Context(), r, &req); err != nil {
		logger.Warn("bad_request",
			zap.String("use_case", "order.createOrder"),
			zap.String("route", route),
			zap.String("path", r.URL.Path),
			zap.Error(err),
		)
		writeError(w, http.StatusBadRequest, err)
		return
	}

	result, err := h.orderService.CreateOrder(r.Context(), appOrder.CreateOrderInput{
		IdempotencyKey: req.IdempotencyKey,
		CustomerID:     req.CustomerID,
		ProductID:      req.ProductID,
		Quantity:       req.Quantity,
		Amount:         req.Amount,
	})
	if err != nil {
		logger.Error("create_order_failed",
			zap.String("use_case", "order.createOrder"),
			zap.String("customer_id", req.CustomerID),
			zap.String("product_id", req.ProductID),
			zap.Int("quantity", req.Quantity),
			zap.Int64("amount", req.Amount),
			zap.Error(err),
		)
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
	route := routeFromContext(r.Context())
	var req processPaymentRequest
	if err := decodeJSON(r.Context(), r, &req); err != nil {
		logger.Warn("bad_request",
			zap.String("use_case", "payment.processPayment"),
			zap.String("route", route),
			zap.String("path", r.URL.Path),
			zap.Error(err),
		)
		writeError(w, http.StatusBadRequest, err)
		return
	}

	status, err := h.paymentService.ProcessPayment(r.Context(), req.OrderID, req.Amount)
	if err != nil {
		logger.Error("payment_failed",
			zap.String("use_case", "payment.processPayment"),
			zap.String("order_id", req.OrderID),
			zap.Int64("amount", req.Amount),
			zap.Error(err),
		)
		writeDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, processPaymentResponse{
		OrderID: req.OrderID,
		Status:  status,
	})
}

func (h *Handler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// withLogging adds a simple access log around the handler.
func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lrw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		logger := logging.FromContext(r.Context())
		sc := trace.SpanContextFromContext(r.Context())
		if sc.IsValid() {
			logger = logging.WithTrace(logger, sc.TraceID().String(), sc.SpanID().String())
		} else {
			rid := uuid.NewString()
			logger = logger.With(zap.String("request_id", rid))
		}

		// store logger in context for downstream logging
		ctx := logging.ContextWithLogger(r.Context(), logger)
		next.ServeHTTP(lrw, r.WithContext(ctx))

		logger.Info("http_request",
			zap.String("method", r.Method),
			zap.String("route", routeFromContext(ctx)),
			zap.String("path", r.URL.Path),
			zap.Int("status", lrw.status),
			zap.Int64("duration_ms", time.Since(start).Milliseconds()),
		)
	})
}

func withTrace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tracer := otel.Tracer("minishop.http")
		route := routeFromContext(r.Context())
		spanName := route
		if spanName == "unknown" {
			spanName = r.Method + " " + r.URL.Path
		}
		template := route
		if idx := strings.Index(template, " "); idx >= 0 {
			template = template[idx+1:]
		}
		if template == "unknown" || template == "" {
			template = r.URL.Path
		}

		ctx, span := tracer.Start(r.Context(),
			spanName,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.route", template),
				attribute.String("http.target", r.URL.Path),
				attribute.String("http.user_agent", r.UserAgent()),
			),
		)
		defer span.End()

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func withMetrics(next http.Handler) http.Handler {
	reqs := prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"method", "route", "status"})
	dur := prometheus.NewHistogramVec(prometheus.HistogramOpts{}, []string{"method", "route", "status"})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lrw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(lrw, r)
		reqs.WithLabelValues(r.Method, routeFromContext(r.Context()), http.StatusText(lrw.status)).Inc()
		dur.WithLabelValues(r.Method, routeFromContext(r.Context()), http.StatusText(lrw.status)).Observe(time.Since(start).Seconds())
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(statusCode int) {
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
	case appOrder.IsValidation(err):
		writeError(w, http.StatusBadRequest, err)
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

type routeKey struct{}

// contextWithRoute stores the stable route template in the context so downstream
// metrics/logging can rely on low-cardinality values.
func contextWithRoute(ctx context.Context, route string) context.Context {
	if route == "" {
		return ctx
	}
	return context.WithValue(ctx, routeKey{}, route)
}

func routeFromContext(ctx context.Context) string {
	if ctx == nil {
		return "unknown"
	}
	if route, ok := ctx.Value(routeKey{}).(string); ok && route != "" {
		return route
	}
	return "unknown"
}
