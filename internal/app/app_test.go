package app

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ahmedelgmal310/sre-observability-slo/internal/httpx"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func envMap(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestRunServesAndShutsDown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	out := &syncBuffer{}
	var gotLatency time.Duration
	done := make(chan int)
	go func() {
		done <- Run(ctx, Options{
			Service:  "svc",
			Version:  "test",
			Getenv:   envMap(map[string]string{"LATENCY_BASE_MS": "7", "LOG_LEVEL": "debug"}),
			Stdout:   out,
			Listener: ln,
			Register: func(mux *http.ServeMux, d Deps) error {
				gotLatency = d.Latency.Base
				d.Instr.Handle(mux, "GET /hello", d.Chaos.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					httpx.WriteJSON(w, http.StatusOK, "hi")
				})))
				return nil
			},
		})
	}()

	base := "http://" + ln.Addr().String()
	deadline := time.Now().Add(5 * time.Second)
	for probe(context.Background(), base+"/readyz") != 0 {
		if time.Now().After(deadline) {
			t.Fatal("service never became ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, p := range []string{"/healthz", "/hello"} {
		resp, err := http.Get(base + p)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: %v %v", p, resp, err)
		}
		_ = resp.Body.Close()
	}
	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("exit code %d: %s", code, out.String())
	}
	if gotLatency != 7*time.Millisecond {
		t.Fatalf("latency from env = %v", gotLatency)
	}
	if !strings.Contains(out.String(), "service started") || !strings.Contains(out.String(), "shutting down") {
		t.Fatalf("lifecycle logs missing: %s", out.String())
	}
}

func TestRunErrors(t *testing.T) {
	ctx := context.Background()
	noop := func(*http.ServeMux, Deps) error { return nil }
	tests := []struct {
		name string
		o    Options
		want int
	}{
		{"bad flag", Options{Service: "svc", Args: []string{"-nope"}, Getenv: envMap(nil), Register: noop}, 2},
		{"bad log level", Options{Service: "svc", Getenv: envMap(map[string]string{"LOG_LEVEL": "loud"}), Register: noop}, 1},
		{"no service name", Options{Getenv: envMap(nil), Register: noop}, 1},
		{"register fails", Options{Service: "svc", Getenv: envMap(nil), Register: func(*http.ServeMux, Deps) error { return errors.New("x") }}, 1},
		{"bad port", Options{Service: "svc", Getenv: envMap(map[string]string{"PORT": "-1"}), Register: noop}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.o.Stdout = &syncBuffer{}
			if got := Run(ctx, tt.o); got != tt.want {
				t.Fatalf("Run() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestHealthcheckFlag(t *testing.T) {
	var ready atomic.Bool
	ready.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	o := Options{Service: "svc", Args: []string{"-healthcheck"}, Getenv: envMap(map[string]string{"PORT": port}), Stdout: &syncBuffer{}}
	if got := Run(context.Background(), o); got != 0 {
		t.Fatalf("healthy probe = %d", got)
	}
	ready.Store(false)
	if got := Run(context.Background(), o); got != 1 {
		t.Fatalf("unready probe = %d", got)
	}
	if probe(context.Background(), "http://127.0.0.1:1/") != 1 || probe(context.Background(), "::bad") != 1 {
		t.Fatal("unreachable probe must fail")
	}
}

func TestEnvMillis(t *testing.T) {
	get := envMap(map[string]string{"A": "12", "B": "x", "C": "-4"})
	if envMillis(get, "A", 0) != 12*time.Millisecond || envMillis(get, "B", time.Second) != time.Second || envMillis(get, "C", 3) != 3 {
		t.Fatal("envMillis parsing")
	}
}
