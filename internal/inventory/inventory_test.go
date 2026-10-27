package inventory

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ahmedelgmal310/sre-observability-slo/internal/httpx"
)

func identity(h http.Handler) http.Handler { return h }

func newServer(t *testing.T, s *Store) *httptest.Server {
	t.Helper()
	in, err := httpx.NewInstrumentation(slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	(&Service{Store: s, Logger: slog.New(slog.DiscardHandler)}).Register(mux, in, identity)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestStoreReserveIsAtomic(t *testing.T) {
	s := NewStore(1, 100)
	var wg sync.WaitGroup
	for range 150 {
		wg.Go(func() { _, _ = s.Reserve("sku-1", 1) })
	}
	wg.Wait()
	if st, _ := s.Get("sku-1"); st.Available != 0 {
		t.Fatalf("available = %d, want 0", st.Available)
	}
	if _, err := s.Reserve("sku-1", 1); err != ErrInsufficient {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.Reserve("nope", 1); err != ErrUnknownSKU {
		t.Fatalf("err = %v", err)
	}
}

func TestHTTP(t *testing.T) {
	srv := newServer(t, NewStore(2, 5))
	tests := []struct {
		method, path, body string
		want               int
	}{
		{http.MethodGet, "/stock/sku-1", "", http.StatusOK},
		{http.MethodGet, "/stock/sku-9", "", http.StatusNotFound},
		{http.MethodPost, "/reserve", `{"sku":"sku-2","qty":2}`, http.StatusOK},
		{http.MethodPost, "/reserve", `{"sku":"sku-2","qty":9}`, http.StatusConflict},
		{http.MethodPost, "/reserve", `{"sku":"sku-9","qty":1}`, http.StatusNotFound},
		{http.MethodPost, "/reserve", `{"sku":"sku-2","qty":0}`, http.StatusBadRequest},
		{http.MethodPost, "/reserve", `not json`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		req, _ := http.NewRequest(tt.method, srv.URL+tt.path, strings.NewReader(tt.body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != tt.want {
			t.Errorf("%s %s %s = %d (%s), want %d", tt.method, tt.path, tt.body, resp.StatusCode, b, tt.want)
		}
	}
}
