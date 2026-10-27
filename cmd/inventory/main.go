// Command inventory owns stock levels and reservations.
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ahmedelgmal310/sre-observability-slo/internal/app"
	"github.com/ahmedelgmal310/sre-observability-slo/internal/inventory"
	"github.com/ahmedelgmal310/sre-observability-slo/internal/latency"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := app.Run(ctx, app.Options{
		Service:        "inventory",
		Version:        version,
		Args:           os.Args[1:],
		Getenv:         os.Getenv,
		Stdout:         os.Stdout,
		DefaultLatency: latency.Profile{Base: 8 * time.Millisecond, Jitter: 12 * time.Millisecond},
		Register: func(mux *http.ServeMux, d app.Deps) error {
			svc := &inventory.Service{
				Store:   inventory.NewStore(50, 1_000_000_000),
				Latency: d.Latency,
				Logger:  d.Logger,
			}
			svc.Register(mux, d.Instr, d.Chaos.Middleware)
			return nil
		},
	})
	stop()
	os.Exit(code)
}
