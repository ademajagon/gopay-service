package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/ademajagon/gopay-service/internal/app"
	"github.com/ademajagon/gopay-service/internal/domain"
)

var (
	httpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "gopay_service",
		Subsystem: "http",
		Name:      "requests_total",
		Help:      "Total HTTP requests partitioned by method, path and status code.",
	}, []string{"method", "path", "status_code"})

	httpRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "gopay_service",
		Subsystem: "http",
		Name:      "request_duration_seconds",
		Help:      "HTTP request duration in seconds.",
		Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
	}, []string{"method", "route"})
)

// Request / Response DTOs

type initiatePaymentRequest struct {
	OrderID        string `json:"order_id"`
	CustomerID     string `json:"customer_id"`
	AmountCents    int64  `json:"amount_cents"`
	Currency       string `json:"currency"`
	IdempotencyKey string `json:"idempotency_key"`
}

type initiatePaymentResponse struct {
	PaymentID string `json:"payment_id"`
	Status    string `json:"status"`
}

type errorResponse struct {
	Error string `json:"error"`
	Code  int    `json:"code"`
}

type Handler struct {
	svc *app.PaymentService
	log *slog.Logger
}

func NewHandler(svc *app.PaymentService, log *slog.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

func (h *Handler) initiatePayment(w http.ResponseWriter, r *http.Request) {
	var body initiatePaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "cannot parse request body", "INVALID_JSON")
		return
	}

	if headerKey := r.Header.Get("idempotency-key"); headerKey != "" {
		body.IdempotencyKey = headerKey
	}

	req := app.InitiatePaymentRequest{
		OrderID:        body.OrderID,
		CustomerID:     body.CustomerID,
		AmountCents:    body.AmountCents,
		Currency:       body.Currency,
		IdempotencyKey: body.IdempotencyKey,
	}

	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "VALIDATION_ERROR")
		return
	}

	result, err := h.svc.InitiatePayment(r.Context(), req)
	if err != nil {
		h.mapError(w, r, err)
		return
	}

	writeJSON(w, http.StatusCreated, initiatePaymentResponse{
		PaymentID: result.PaymentID,
		Status:    result.Status,
	})
}

// error mapping
func (h *Handler) mapError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "payment not found", "NOT_FOUND")
	case errors.Is(err, domain.ErrVersionConflict):
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusConflict, "concurrent modification, please retry", "CONFLICT")
	case errors.Is(err, domain.ErrInvalidTransition):
		writeError(w, http.StatusUnprocessableEntity, err.Error(), "INVALID_STATE_TRANSITION")

	default:
		h.log.ErrorContext(r.Context(), "unhandled error in HTTP handler",
			"err", err,
			"path", r.URL.Path,
			"method", r.Method,
		)
		writeError(w, http.StatusInternalServerError, "an unexcepted error occurred", "INTERNAL_ERROR")
	}
}

// Server wraps *http.Server with graceful shutdown
type Server struct {
	inner   *http.Server
	log     *slog.Logger
	timeout time.Duration
}

// ServerConfig groups all HTTP server tuning parameters
type ServerConfig struct {
	Addr            string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
}

// ReadinessCheck is a function that confirms a dependency is reachable
type ReadinessCheck func(ctx context.Context) error

func NewServer(cfg ServerConfig, h *Handler, checks []ReadinessCheck, log *slog.Logger) *Server {
	r := chi.NewRouter()

	r.Use(middleware.RealIP)
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(requestLogger(log))
	r.Use(prometheusMiddleware())

	// k8s observability
	r.Get("/healthz/live", livenessHandler())
	r.Get("/healthz/ready", readinessHandler(checks))

	// routes
	r.Route("/v1/payments", func(r chi.Router) {
		r.Post("/", h.initiatePayment)
		//r.Get("/{paymentID}")
	})

	return &Server{
		inner: &http.Server{
			Addr:         cfg.Addr,
			Handler:      r,
			ReadTimeout:  cfg.ReadTimeout,
			WriteTimeout: cfg.WriteTimeout,
			IdleTimeout:  cfg.IdleTimeout,
		},
		log:     log,
		timeout: cfg.ShutdownTimeout,
	}
}

func (s *Server) Start() error {
	s.log.Info("HTTP server listening", "addr", s.inner.Addr)
	if err := s.inner.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	shutCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	s.log.Info("HTTP server shutting down gracefully")
	return s.inner.Shutdown(shutCtx)
}

// health probes
// k8s three probe types

func livenessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// confirms the HTTP server is running
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}
}

func readinessHandler(checks []ReadinessCheck) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		for _, check := range checks {
			if err := check(ctx); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				body, _ := json.Marshal(map[string]string{
					"status": "degraded",
					"error":  err.Error(),
				})
				_, _ = w.Write(body)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}
}

func requestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			defer func() {
				log.InfoContext(r.Context(), "http request",
					"method", r.Method,
					"path", r.URL.Path,
					"status", ww.Status(),
					"duration", time.Since(start).Milliseconds(),
					"request_id", middleware.GetReqID(r.Context()),
					"bytes", ww.BytesWritten())
			}()

			next.ServeHTTP(ww, r)
		})
	}
}

// records RED metrics per route
func prometheusMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			defer func() {
				route := chi.RouteContext(r.Context()).RoutePattern()
				if route == "" {
					route = "unknown"
				}

				statusCode := fmt.Sprintf("%d", ww.Status())
				httpRequestsTotal.WithLabelValues(r.Method, route, statusCode).Inc()
				httpRequestDuration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
			}()

			next.ServeHTTP(ww, r)
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		return
	}
}

func writeError(w http.ResponseWriter, status int, message, code string) {
	writeJSON(w, status, errorResponse{Error: message, Code: status})
}
