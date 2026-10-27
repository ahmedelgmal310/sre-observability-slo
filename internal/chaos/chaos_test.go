package chaos

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel/baggage"
)

func withBaggage(t *testing.T, raw string) context.Context {
	t.Helper()
	b, err := baggage.Parse(raw)
	if err != nil {
		t.Fatalf("parse baggage: %v", err)
	}
	return baggage.ContextWithBaggage(context.Background(), b)
}

func TestResolve(t *testing.T) {
	base := Fault{ErrorRate: 0.1}
	tests := []struct {
		name    string
		enabled bool
		bag     string
		want    Fault
	}{
		{"disabled ignores baggage", false, "chaos.target=inventory,chaos.error_rate=1", base},
		{"no baggage keeps base", true, "", base},
		{"other target keeps base", true, "chaos.target=orders,chaos.error_rate=1", base},
		{"exact target", true, "chaos.target=inventory,chaos.error_rate=0.5,chaos.latency_ms=20", Fault{0.5, 20 * time.Millisecond}},
		{"list target", true, "chaos.target=orders|inventory,chaos.error_rate=0.7", Fault{0.7, 0}},
		{"wildcard", true, "chaos.target=*,chaos.latency_ms=5", Fault{0.1, 5 * time.Millisecond}},
		{"clamped", true, "chaos.target=*,chaos.error_rate=7,chaos.latency_ms=-3", Fault{1, 0}},
		{"garbage values ignored", true, "chaos.target=*,chaos.error_rate=abc", base},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.bag != "" {
				ctx = withBaggage(t, tt.bag)
			}
			got := New("inventory", tt.enabled, base).Resolve(ctx)
			if got != tt.want {
				t.Fatalf("Resolve() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestFromEnv(t *testing.T) {
	env := map[string]string{"CHAOS_ENABLED": "true", "CHAOS_ERROR_RATE": "0.25", "CHAOS_LATENCY_MS": "15"}
	i := FromEnv("orders", func(k string) string { return env[k] })
	if !i.enabled || i.base != (Fault{0.25, 15 * time.Millisecond}) {
		t.Fatalf("unexpected injector %+v", i)
	}
	if FromEnv("orders", func(string) string { return "" }).base.Active() {
		t.Fatal("empty env must not inject faults")
	}
}

func serve(i *Injector, ctx context.Context) *httptest.ResponseRecorder {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	i.Middleware(next).ServeHTTP(rec, req)
	return rec
}

func TestMiddleware(t *testing.T) {
	t.Run("passthrough", func(t *testing.T) {
		if rec := serve(New("svc", false, Fault{}), context.Background()); rec.Code != http.StatusTeapot {
			t.Fatalf("code = %d", rec.Code)
		}
	})
	t.Run("injected error", func(t *testing.T) {
		i := New("svc", false, Fault{ErrorRate: 0.5})
		i.rnd = func() float64 { return 0.1 }
		if rec := serve(i, context.Background()); rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("code = %d", rec.Code)
		}
	})
	t.Run("error not drawn", func(t *testing.T) {
		i := New("svc", false, Fault{ErrorRate: 0.5})
		i.rnd = func() float64 { return 0.9 }
		if rec := serve(i, context.Background()); rec.Code != http.StatusTeapot {
			t.Fatalf("code = %d", rec.Code)
		}
	})
	t.Run("latency", func(t *testing.T) {
		start := time.Now()
		rec := serve(New("svc", false, Fault{Latency: 30 * time.Millisecond}), context.Background())
		if rec.Code != http.StatusTeapot || time.Since(start) < 30*time.Millisecond {
			t.Fatalf("code = %d after %v", rec.Code, time.Since(start))
		}
	})
	t.Run("latency cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		rec := serve(New("svc", false, Fault{Latency: time.Second}), ctx)
		if rec.Code != http.StatusOK { // nothing written
			t.Fatalf("code = %d", rec.Code)
		}
	})
}
