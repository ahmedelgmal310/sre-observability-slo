package gateway

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github.com/ahmedelgmal310/sre-observability-slo/internal/chaos"
	"github.com/ahmedelgmal310/sre-observability-slo/internal/httpx"
	"github.com/ahmedelgmal310/sre-observability-slo/internal/inventory"
	"github.com/ahmedelgmal310/sre-observability-slo/internal/orders"
)

// chain starts inventory, orders and gateway wired together, each with a
// chaos injector that honours baggage, like the Compose deployment.
func chain(t *testing.T) string {
	t.Helper()
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	log := slog.New(slog.DiscardHandler)
	in, err := httpx.NewInstrumentation(log)
	if err != nil {
		t.Fatal(err)
	}
	start := func(name string, register func(*http.ServeMux, func(http.Handler) http.Handler)) string {
		mux := http.NewServeMux()
		register(mux, chaos.New(name, true, chaos.Fault{}).Middleware)
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)
		return srv.URL
	}
	invURL := start("inventory", func(m *http.ServeMux, w func(http.Handler) http.Handler) {
		(&inventory.Service{Store: inventory.NewStore(3, 100), Logger: log}).Register(m, in, w)
	})
	ordURL := start("orders", func(m *http.ServeMux, w func(http.Handler) http.Handler) {
		(&orders.Service{Repo: orders.NewRepo(), Client: httpx.NewClient(0), InventoryURL: invURL, Logger: log}).Register(m, in, w)
	})
	return start("gateway", func(m *http.ServeMux, w func(http.Handler) http.Handler) {
		(&Service{Client: httpx.NewClient(0), OrdersURL: ordURL, Logger: log}).Register(m, in, w)
	})
}

func do(t *testing.T, method, url, body string, header http.Header) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestEndToEnd(t *testing.T) {
	gw := chain(t)

	code, body := do(t, http.MethodPost, gw+"/api/orders", `{"sku":"sku-1","qty":2}`, nil)
	if code != http.StatusCreated {
		t.Fatalf("create = %d %s", code, body)
	}
	var o orders.Order
	if err := json.Unmarshal(body, &o); err != nil {
		t.Fatal(err)
	}
	code, body = do(t, http.MethodGet, gw+"/api/orders/"+o.ID, "", nil)
	if code != http.StatusOK || !strings.Contains(string(body), `"available":98`) {
		t.Fatalf("get = %d %s", code, body)
	}

	cases := []struct {
		name, method, path, body string
		want                     int
	}{
		{"invalid body", http.MethodPost, "/api/orders", `{}`, http.StatusBadRequest},
		{"unknown sku passes 404", http.MethodPost, "/api/orders", `{"sku":"nope","qty":1}`, http.StatusNotFound},
		{"insufficient passes 409", http.MethodPost, "/api/orders", `{"sku":"sku-2","qty":1000}`, http.StatusConflict},
		{"missing order", http.MethodGet, "/api/orders/ord-404", "", http.StatusNotFound},
	}
	for _, c := range cases {
		if code, body := do(t, c.method, gw+c.path, c.body, nil); code != c.want {
			t.Errorf("%s: %d %s, want %d", c.name, code, body, c.want)
		}
	}
}

func TestChaosBaggageReachesLeaf(t *testing.T) {
	gw := chain(t)
	h := http.Header{"Baggage": {"chaos.target=inventory,chaos.error_rate=1"}}

	if code, body := do(t, http.MethodPost, gw+"/api/orders", `{"sku":"sku-1","qty":1}`, h); code != http.StatusBadGateway {
		t.Fatalf("create under chaos = %d %s, want 502", code, body)
	}
	// Without baggage the same request succeeds: the fault is per request.
	if code, body := do(t, http.MethodPost, gw+"/api/orders", `{"sku":"sku-1","qty":1}`, nil); code != http.StatusCreated {
		t.Fatalf("create without chaos = %d %s", code, body)
	}
}

func TestOrdersUnreachable(t *testing.T) {
	in, _ := httpx.NewInstrumentation(slog.New(slog.DiscardHandler))
	mux := http.NewServeMux()
	(&Service{Client: httpx.NewClient(0), OrdersURL: "http://127.0.0.1:1", Logger: slog.New(slog.DiscardHandler)}).
		Register(mux, in, func(h http.Handler) http.Handler { return h })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	if code, _ := do(t, http.MethodGet, srv.URL+"/api/orders/x", "", nil); code != http.StatusBadGateway {
		t.Fatalf("code = %d", code)
	}
}
