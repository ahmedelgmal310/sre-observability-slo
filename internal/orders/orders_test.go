package orders

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ahmedelgmal310/sre-observability-slo/internal/httpx"
	"github.com/ahmedelgmal310/sre-observability-slo/internal/inventory"
)

func identity(h http.Handler) http.Handler { return h }

// fakeInventory answers /reserve and /stock with the configured statuses.
func fakeInventory(t *testing.T, reserveStatus, stockStatus *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/reserve" && *reserveStatus == http.StatusOK:
			httpx.WriteJSON(w, http.StatusOK, inventory.Reservation{ID: "rsv-1", SKU: "sku-1", Qty: 1})
		case r.URL.Path == "/reserve":
			httpx.WriteError(w, *reserveStatus, "x")
		case *stockStatus == http.StatusOK:
			httpx.WriteJSON(w, http.StatusOK, inventory.Stock{SKU: "sku-1", Available: 7})
		default:
			httpx.WriteError(w, *stockStatus, "x")
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newServer(t *testing.T, inventoryURL string) *httptest.Server {
	t.Helper()
	in, err := httpx.NewInstrumentation(slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	svc := &Service{Repo: NewRepo(), Client: httpx.NewClient(0), InventoryURL: inventoryURL, Logger: slog.New(slog.DiscardHandler)}
	svc.Register(mux, in, identity)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func post(t *testing.T, url, body string) (*http.Response, Order) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var o Order
	_ = json.NewDecoder(resp.Body).Decode(&o)
	return resp, o
}

func get(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestCreateAndGet(t *testing.T) {
	reserve, stock := http.StatusOK, http.StatusOK
	srv := newServer(t, fakeInventory(t, &reserve, &stock).URL)

	resp, o := post(t, srv.URL+"/orders", `{"sku":"sku-1","qty":1}`)
	if resp.StatusCode != http.StatusCreated || o.ID == "" || o.ReservationID != "rsv-1" {
		t.Fatalf("create = %d %+v", resp.StatusCode, o)
	}
	if code := get(t, srv.URL+"/orders/"+o.ID); code != http.StatusOK {
		t.Fatalf("get = %d", code)
	}
	if code := get(t, srv.URL+"/orders/ord-missing"); code != http.StatusNotFound {
		t.Fatalf("missing = %d", code)
	}

	stock = http.StatusServiceUnavailable
	if code := get(t, srv.URL+"/orders/"+o.ID); code != http.StatusBadGateway {
		t.Fatalf("stock down = %d", code)
	}
}

func TestCreateFailures(t *testing.T) {
	reserve, stock := http.StatusOK, http.StatusOK
	srv := newServer(t, fakeInventory(t, &reserve, &stock).URL)

	if resp, _ := post(t, srv.URL+"/orders", `{"sku":""}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid = %d", resp.StatusCode)
	}
	reserve = http.StatusConflict
	if resp, _ := post(t, srv.URL+"/orders", `{"sku":"sku-1","qty":1}`); resp.StatusCode != http.StatusConflict {
		t.Fatalf("conflict = %d", resp.StatusCode)
	}
	reserve = http.StatusServiceUnavailable
	if resp, _ := post(t, srv.URL+"/orders", `{"sku":"sku-1","qty":1}`); resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("inventory down = %d", resp.StatusCode)
	}
}

func TestRepoBounded(t *testing.T) {
	r := NewRepo()
	for range maxOrders + 1 {
		r.Save(Order{SKU: "s"})
	}
	if len(r.orders) > maxOrders {
		t.Fatalf("repo grew to %d", len(r.orders))
	}
}
