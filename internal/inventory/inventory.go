// Package inventory is the leaf service: it owns stock levels and reservations.
package inventory

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/ahmedelgmal310/sre-observability-slo/internal/httpx"
	"github.com/ahmedelgmal310/sre-observability-slo/internal/latency"
)

// Stock is the availability of one SKU.
type Stock struct {
	SKU       string `json:"sku"`
	Available int    `json:"available"`
}

// ReserveRequest asks to hold qty units of sku.
type ReserveRequest struct {
	SKU string `json:"sku"`
	Qty int    `json:"qty"`
}

// Reservation confirms a hold.
type Reservation struct {
	ID        string `json:"reservation_id"`
	SKU       string `json:"sku"`
	Qty       int    `json:"qty"`
	Remaining int    `json:"remaining"`
}

// Store is a concurrency-safe in-memory stock ledger.
type Store struct {
	mu    sync.Mutex
	stock map[string]int
	seq   atomic.Uint64
}

// NewStore seeds n SKUs named sku-1..sku-n with qty units each.
func NewStore(n, qty int) *Store {
	s := &Store{stock: make(map[string]int, n)}
	for i := 1; i <= n; i++ {
		s.stock[fmt.Sprintf("sku-%d", i)] = qty
	}
	return s
}

// Get returns the stock for sku.
func (s *Store) Get(sku string) (Stock, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, ok := s.stock[sku]
	return Stock{SKU: sku, Available: q}, ok
}

// Errors returned by Reserve.
var (
	ErrUnknownSKU   = fmt.Errorf("unknown sku")
	ErrInsufficient = fmt.Errorf("insufficient stock")
)

// Reserve decrements stock atomically.
func (s *Store) Reserve(sku string, qty int) (Reservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, ok := s.stock[sku]
	if !ok {
		return Reservation{}, ErrUnknownSKU
	}
	if q < qty {
		return Reservation{}, ErrInsufficient
	}
	s.stock[sku] = q - qty
	return Reservation{ID: fmt.Sprintf("rsv-%d", s.seq.Add(1)), SKU: sku, Qty: qty, Remaining: q - qty}, nil
}

// Service exposes the store over HTTP.
type Service struct {
	Store   *Store
	Latency latency.Profile
	Logger  *slog.Logger
}

// Register mounts the routes on mux through in.
func (s *Service) Register(mux *http.ServeMux, in *httpx.Instrumentation, wrap func(http.Handler) http.Handler) {
	in.Handle(mux, "GET /stock/{sku}", wrap(http.HandlerFunc(s.getStock)))
	in.Handle(mux, "POST /reserve", wrap(http.HandlerFunc(s.reserve)))
}

func (s *Service) getStock(w http.ResponseWriter, r *http.Request) {
	if err := s.Latency.Sleep(r.Context()); err != nil {
		return
	}
	st, ok := s.Store.Get(r.PathValue("sku"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "unknown sku")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, st)
}

func (s *Service) reserve(w http.ResponseWriter, r *http.Request) {
	var req ReserveRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.SKU == "" || req.Qty <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "body must be {\"sku\": string, \"qty\": positive int}")
		return
	}
	if err := s.Latency.Sleep(r.Context()); err != nil {
		return
	}
	res, err := s.Store.Reserve(req.SKU, req.Qty)
	switch err {
	case nil:
		httpx.WriteJSON(w, http.StatusOK, res)
	case ErrUnknownSKU:
		httpx.WriteError(w, http.StatusNotFound, err.Error())
	default:
		s.Logger.WarnContext(r.Context(), "reservation rejected", slog.String("sku", req.SKU), slog.Int("qty", req.Qty))
		httpx.WriteError(w, http.StatusConflict, err.Error())
	}
}
