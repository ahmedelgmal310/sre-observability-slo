// Package gateway is the public edge. Its availability and latency are what
// the SLOs measure.
package gateway

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/ahmedelgmal310/sre-observability-slo/internal/httpx"
	"github.com/ahmedelgmal310/sre-observability-slo/internal/latency"
	"github.com/ahmedelgmal310/sre-observability-slo/internal/orders"
)

// Service proxies the public API to the orders service.
type Service struct {
	Client    *http.Client
	OrdersURL string
	Latency   latency.Profile
	Logger    *slog.Logger
}

// Register mounts the routes on mux through in.
func (s *Service) Register(mux *http.ServeMux, in *httpx.Instrumentation, wrap func(http.Handler) http.Handler) {
	in.Handle(mux, "POST /api/orders", wrap(http.HandlerFunc(s.create)))
	in.Handle(mux, "GET /api/orders/{id}", wrap(http.HandlerFunc(s.get)))
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req orders.CreateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.SKU == "" || req.Qty <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "body must be {\"sku\": string, \"qty\": positive int}")
		return
	}
	if err := s.Latency.Sleep(ctx); err != nil {
		return
	}
	var o orders.Order
	if err := httpx.DoJSON(ctx, s.Client, http.MethodPost, s.OrdersURL+"/orders", req, &o); err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, o)
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.Latency.Sleep(ctx); err != nil {
		return
	}
	var o orders.Order
	if err := httpx.DoJSON(ctx, s.Client, http.MethodGet, s.OrdersURL+"/orders/"+r.PathValue("id"), nil, &o); err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, o)
}

func (s *Service) fail(w http.ResponseWriter, r *http.Request, err error) {
	status := httpx.UpstreamStatus(err)
	if status >= http.StatusInternalServerError {
		s.Logger.ErrorContext(r.Context(), "upstream call failed", slog.String("upstream", "orders"), slog.String("error", err.Error()))
		httpx.WriteError(w, status, "upstream unavailable")
		return
	}
	httpx.WriteError(w, status, http.StatusText(status))
}
