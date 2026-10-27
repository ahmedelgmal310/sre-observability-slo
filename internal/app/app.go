// Package app is the process lifecycle shared by every service: config from
// the environment, telemetry setup, health endpoints, graceful shutdown and a
// -healthcheck mode for container health probes in distroless images.
package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/ahmedelgmal310/sre-observability-slo/internal/chaos"
	"github.com/ahmedelgmal310/sre-observability-slo/internal/httpx"
	"github.com/ahmedelgmal310/sre-observability-slo/internal/latency"
	"github.com/ahmedelgmal310/sre-observability-slo/internal/logging"
	"github.com/ahmedelgmal310/sre-observability-slo/internal/telemetry"
)

// Deps are handed to a service so it can mount its routes.
type Deps struct {
	Logger  *slog.Logger
	Instr   *httpx.Instrumentation
	Chaos   *chaos.Injector
	Latency latency.Profile
	Client  *http.Client
	Getenv  func(string) string
}

// Options configure Run.
type Options struct {
	Service string
	Version string
	Args    []string
	Getenv  func(string) string
	Stdout  io.Writer
	// DefaultLatency is used when LATENCY_BASE_MS / LATENCY_JITTER_MS are unset.
	DefaultLatency latency.Profile
	// Listener overrides PORT; used by tests.
	Listener net.Listener
	Register func(mux *http.ServeMux, d Deps) error
}

// Run starts the service and blocks until ctx is cancelled. It returns the
// process exit code.
func Run(ctx context.Context, o Options) int {
	fs := flag.NewFlagSet(o.Service, flag.ContinueOnError)
	fs.SetOutput(o.Stdout)
	health := fs.Bool("healthcheck", false, "probe /readyz on PORT and exit 0 when ready")
	if err := fs.Parse(o.Args); err != nil {
		return 2
	}
	port := envOr(o.Getenv, "PORT", "8080")
	if *health {
		return probe(ctx, "http://127.0.0.1:"+port+"/readyz")
	}
	if err := serve(ctx, o, port); err != nil {
		_, _ = fmt.Fprintf(o.Stdout, `{"level":"ERROR","service":%q,"msg":%q}`+"\n", o.Service, err.Error())
		return 1
	}
	return 0
}

func serve(ctx context.Context, o Options, port string) error {
	tp, err := telemetry.Setup(ctx, telemetry.Config{
		ServiceName:    o.Service,
		ServiceVersion: o.Version,
		Export:         o.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "",
	})
	if err != nil {
		return err
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = tp.Shutdown(sctx)
	}()

	var level slog.Level
	if err := level.UnmarshalText([]byte(envOr(o.Getenv, "LOG_LEVEL", "INFO"))); err != nil {
		return fmt.Errorf("LOG_LEVEL: %w", err)
	}
	logger := logging.New(o.Service, o.Stdout, level, tp.Logger)

	instr, err := httpx.NewInstrumentation(logger)
	if err != nil {
		return err
	}
	deps := Deps{
		Logger: logger,
		Instr:  instr,
		Chaos:  chaos.FromEnv(o.Service, o.Getenv),
		Latency: latency.Profile{
			Base:   envMillis(o.Getenv, "LATENCY_BASE_MS", o.DefaultLatency.Base),
			Jitter: envMillis(o.Getenv, "LATENCY_JITTER_MS", o.DefaultLatency.Jitter),
		},
		Client: httpx.NewClient(5 * time.Second),
		Getenv: o.Getenv,
	}

	var ready atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			httpx.WriteError(w, http.StatusServiceUnavailable, "not ready")
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	if err := o.Register(mux, deps); err != nil {
		return err
	}

	ln := o.Listener
	if ln == nil {
		var lc net.ListenConfig
		if ln, err = lc.Listen(ctx, "tcp", ":"+port); err != nil {
			return err
		}
	}
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	ready.Store(true)
	logger.InfoContext(ctx, "service started", slog.String("addr", ln.Addr().String()), slog.String("version", o.Version))

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	ready.Store(false)
	logger.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func probe(ctx context.Context, url string) int {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 1
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func envOr(getenv func(string) string, key, def string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return def
}

func envMillis(getenv func(string) string, key string, def time.Duration) time.Duration {
	v, err := strconv.Atoi(getenv(key))
	if err != nil || v < 0 {
		return def
	}
	return time.Duration(v) * time.Millisecond
}
