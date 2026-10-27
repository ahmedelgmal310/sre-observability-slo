// Command orders creates orders and reserves stock in inventory.
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ahmedelgmal310/sre-observability-slo/internal/app"
	"github.com/ahmedelgmal310/sre-observability-slo/internal/latency"
	"github.com/ahmedelgmal310/sre-observability-slo/internal/orders"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := app.Run(ctx, app.Options{
		Service:        "orders",
		Version:        version,
		Args:           os.Args[1:],
		Getenv:         os.Getenv,
		Stdout:         os.Stdout,
		DefaultLatency: latency.Profile{Base: 3 * time.Millisecond, Jitter: 4 * time.Millisecond},
		Register: func(mux *http.ServeMux, d app.Deps) error {
			svc := &orders.Service{
				Repo:         orders.NewRepo(),
				Client:       d.Client,
				InventoryURL: d.Getenv("INVENTORY_URL"),
				Latency:      d.Latency,
				Logger:       d.Logger,
			}
			svc.Register(mux, d.Instr, d.Chaos.Middleware)
			return nil
		},
	})
	stop()
	os.Exit(code)
}
