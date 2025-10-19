package httppresentation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	appOrder "github.com/Zhima-Mochi/minishop-observability/app/internal/application/order"
	appPayment "github.com/Zhima-Mochi/minishop-observability/app/internal/application/payment"
	domainInventory "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/inventory"
	domainOrder "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/order"
	domainPayment "github.com/Zhima-Mochi/minishop-observability/app/internal/domain/payment"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability"
	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability/logctx"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type Handler struct {
	orderService   *appOrder.Service
	paymentService *appPayment.Service
	log            observability.Logger
	tel            observability.Telemetry
}

const (
	componentHTTPHandler = "http_server"
	headerRequestID      = "X-Request-ID"
	headerTenantID       = "X-Tenant-ID"
)

func NewHandler(orderSvc *appOrder.Service, paymentSvc *appPayment.Service, logger observability.Logger,
	tel observability.Telemetry,
) *Handler {
	baseLogger := logger
	if baseLogger == nil {
		baseLogger = observability.NopLogger()
	}
	return &Handler{
		orderService:   orderSvc,
		paymentService: paymentSvc,
		log:            baseLogger.With(observability.F("component", componentHTTPHandler)),
		tel:            tel,
	}
}

func (h *Handler) Router() http.Handler {
	mux := http.NewServeMux()

	// Wire each route with middlewares:
	// Trace → ObservabilityMiddleware (request logger) → HTTP metrics → Access log → Handler
	h.muxHandle(mux, http.MethodPost, "/order", h.handleCreateOrder)
	h.muxHandle(mux, http.MethodPost, "/payment/pay", h.handleProcessPayment)
	h.muxHandle(mux, http.MethodGet, "/health", h.handleHealth)

	return mux
}

func (h *Handler) muxHandle(mux *http.ServeMux, method, route string, handler http.HandlerFunc) {
	mux.HandleFunc(route, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		// Store stable route template for low-cardinality labels
		ctx := contextWithRoute(r.Context(), route)
		r = r.WithContext(ctx)

		// Wrap: Trace → Request Logger → Metrics → Access Log → Handler
		wrapped := h.withTrace(
			ObservabilityMiddleware(
				logctx.FromOr(ctx, h.log),
				func(r *http.Request) string {
					return r.Header.Get(headerRequestID)
				},
				func(r *http.Request) string {
					return r.Header.Get(headerTenantID)
				},
				h.tel,
			)(
				h.withAccessLog(
					h.withHTTPMetrics(http.HandlerFunc(handler)),
				),
			),
		)
		wrapped.ServeHTTP(w, r)
	})
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
	var req createOrderRequest
	if err := decodeJSON(r.Context(), r, &req); err != nil {
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
	var req processPaymentRequest
	if err := decodeJSON(r.Context(), r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	status, err := h.paymentService.ProcessPayment(r.Context(), req.OrderID, req.Amount)
	if err != nil {
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

// withAccessLog writes a single access log after the handler completes.
// It relies on the request-scoped logger already injected by ObservabilityMiddleware.
func (h *Handler) withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lrw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(lrw, r)

		logctx.FromOr(r.Context(), h.log).Info("http_access",
			observability.F("method", r.Method),
			observability.F("route", routeFromContext(r.Context())),
			observability.F("path", r.URL.Path),
			observability.F("status", lrw.status),
			observability.F("latency_ms", time.Since(start).Milliseconds()),
		)
	})
}

// withTrace creates a server span for the request using OTel and W3C propagation.
func (h *Handler) withTrace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tracer := otel.Tracer("minishop.http")
		parentCtx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))

		route := routeFromContext(parentCtx)
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

		ctxWithSpan, span := tracer.Start(parentCtx,
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

		next.ServeHTTP(w, r.WithContext(ctxWithSpan))
	})
}

// withHTTPMetrics records RED-ish HTTP metrics using injected vectors.
// DO NOT new metrics inside the middleware.
func (h *Handler) withHTTPMetrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lrw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(lrw, r)

		if h.tel != nil {
			h.tel.Counter("http_requests_total").Add(1, observability.L("method", r.Method), observability.L("route", routeFromContext(r.Context())), observability.L("status", strconv.Itoa(lrw.status)))
		}
		if h.tel != nil {
			h.tel.Histogram("http_request_duration_seconds").Observe(time.Since(start).Seconds(), observability.L("method", r.Method), observability.L("route", routeFromContext(r.Context())), observability.L("status", strconv.Itoa(lrw.status)))
		}
	})
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
