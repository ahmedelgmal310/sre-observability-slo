// Package orders creates and serves orders, reserving stock in inventory.
package orders

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/ahmedelgmal310/sre-observability-slo/internal/httpx"
	"github.com/ahmedelgmal310/sre-observability-slo/internal/inventory"
	"github.com/ahmedelgmal310/sre-observability-slo/internal/latency"
)

// maxOrders bounds memory use of the in-memory repository.
const maxOrders = 100_000

// CreateRequest is the body of POST /orders.
type CreateRequest struct {
	SKU string `json:"sku"`
	Qty int    `json:"qty"`
}

// Order is a confirmed order.
type Order struct {
	ID            string `json:"id"`
	SKU           string `json:"sku"`
	Qty           int    `json:"qty"`
	ReservationID string `json:"reservation_id"`
	Status        string `json:"status"`
	Available     *int   `json:"available,omitempty"`
}

// Repo is an in-memory order repository.
type Repo struct {
	mu     sync.RWMutex
	orders map[string]Order
	seq    atomic.Uint64
}

// NewRepo returns an empty repository.
func NewRepo() *Repo { return &Repo{orders: make(map[string]Order)} }

// Save assigns an ID and stores o.
func (r *Repo) Save(o Order) Order {
	o.ID = fmt.Sprintf("ord-%d", r.seq.Add(1))
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.orders) >= maxOrders {
		clear(r.orders)
	}
	r.orders[o.ID] = o
	return o
}

// Get returns the order with id.
func (r *Repo) Get(id string) (Order, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	o, ok := r.orders[id]
	return o, ok
}

// Service is the orders HTTP API.
type Service struct {
	Repo         *Repo
	Client       *http.Client
	InventoryURL string
	Latency      latency.Profile
	Logger       *slog.Logger
}

// Register mounts the routes on mux through in.
func (s *Service) Register(mux *http.ServeMux, in *httpx.Instrumentation, wrap func(http.Handler) http.Handler) {
	in.Handle(mux, "POST /orders", wrap(http.HandlerFunc(s.create)))
	in.Handle(mux, "GET /orders/{id}", wrap(http.HandlerFunc(s.get)))
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req CreateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.SKU == "" || req.Qty <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "body must be {\"sku\": string, \"qty\": positive int}")
		return
	}
	if err := s.Latency.Sleep(ctx); err != nil {
		return
	}
	var res inventory.Reservation
	err := httpx.DoJSON(ctx, s.Client, http.MethodPost, s.InventoryURL+"/reserve",
		inventory.ReserveRequest(req), &res)
	if err != nil {
		status := httpx.UpstreamStatus(err)
		s.Logger.LogAttrs(ctx, levelFor(status), "reservation failed",
			slog.String("sku", req.SKU), slog.Int("upstream_status", status), slog.String("error", err.Error()))
		httpx.WriteError(w, status, "reservation failed")
		return
	}
	o := s.Repo.Save(Order{SKU: req.SKU, Qty: req.Qty, ReservationID: res.ID, Status: "confirmed"})
	s.Logger.InfoContext(ctx, "order created", slog.String("order_id", o.ID), slog.String("sku", o.SKU))
	httpx.WriteJSON(w, http.StatusCreated, o)
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.Latency.Sleep(ctx); err != nil {
		return
	}
	o, ok := s.Repo.Get(r.PathValue("id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "order not found")
		return
	}
	var st inventory.Stock
	if err := httpx.DoJSON(ctx, s.Client, http.MethodGet, s.InventoryURL+"/stock/"+o.SKU, nil, &st); err != nil {
		status := httpx.UpstreamStatus(err)
		s.Logger.LogAttrs(ctx, levelFor(status), "stock lookup failed",
			slog.String("order_id", o.ID), slog.Int("upstream_status", status), slog.String("error", err.Error()))
		httpx.WriteError(w, http.StatusBadGateway, "stock lookup failed")
		return
	}
	o.Available = &st.Available
	httpx.WriteJSON(w, http.StatusOK, o)
}

func levelFor(status int) slog.Level {
	if status >= http.StatusInternalServerError {
		return slog.LevelError
	}
	return slog.LevelWarn
}
