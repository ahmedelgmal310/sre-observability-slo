// Command gateway is the public edge of the Lighthouse system.
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ahmedelgmal310/sre-observability-slo/internal/app"
	"github.com/ahmedelgmal310/sre-observability-slo/internal/gateway"
	"github.com/ahmedelgmal310/sre-observability-slo/internal/latency"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := app.Run(ctx, app.Options{
		Service:        "gateway",
		Version:        version,
		Args:           os.Args[1:],
		Getenv:         os.Getenv,
		Stdout:         os.Stdout,
		DefaultLatency: latency.Profile{Base: time.Millisecond, Jitter: time.Millisecond},
		Register: func(mux *http.ServeMux, d app.Deps) error {
			svc := &gateway.Service{
				Client:    d.Client,
				OrdersURL: d.Getenv("ORDERS_URL"),
				Latency:   d.Latency,
				Logger:    d.Logger,
			}
			svc.Register(mux, d.Instr, d.Chaos.Middleware)
			return nil
		},
	})
	stop()
	os.Exit(code)
}
