// Package chaos injects errors and latency into a service so that failure
// modes can be exercised on demand.
//
// Faults come from two places:
//
//   - Static defaults from the environment (CHAOS_ERROR_RATE, CHAOS_LATENCY_MS),
//     which always apply. Useful to degrade one service for a whole run.
//
//   - Per-request W3C baggage, honoured only when CHAOS_ENABLED=true. Baggage is
//     propagated by OpenTelemetry, so a client can target a service deep in the
//     call graph from the edge:
//
//     baggage: chaos.target=inventory,chaos.error_rate=0.5,chaos.latency_ms=200
//
// chaos.target is a "|"-separated list of service names, or "*" for all.
package chaos

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"
)

// Baggage member keys understood by the injector.
const (
	KeyTarget    = "chaos.target"
	KeyErrorRate = "chaos.error_rate"
	KeyLatencyMS = "chaos.latency_ms"
)

// Fault is the failure applied to a single request.
type Fault struct {
	ErrorRate float64       // probability in [0,1] of answering 503
	Latency   time.Duration // extra delay before handling
}

// Active reports whether the fault changes behaviour at all.
func (f Fault) Active() bool { return f.ErrorRate > 0 || f.Latency > 0 }

// Injector decides and applies faults for one service.
type Injector struct {
	service string
	enabled bool
	base    Fault
	rnd     func() float64
}

// New returns an injector. enabled controls whether baggage overrides are
// honoured; base is always applied.
func New(service string, enabled bool, base Fault) *Injector {
	return &Injector{service: service, enabled: enabled, base: clamp(base), rnd: rand.Float64}
}

// FromEnv builds an injector from CHAOS_ENABLED, CHAOS_ERROR_RATE and
// CHAOS_LATENCY_MS read through getenv.
func FromEnv(service string, getenv func(string) string) *Injector {
	enabled, _ := strconv.ParseBool(getenv("CHAOS_ENABLED"))
	rate, _ := strconv.ParseFloat(getenv("CHAOS_ERROR_RATE"), 64)
	ms, _ := strconv.Atoi(getenv("CHAOS_LATENCY_MS"))
	return New(service, enabled, Fault{ErrorRate: rate, Latency: time.Duration(ms) * time.Millisecond})
}

// Resolve returns the fault for the request carried by ctx.
func (i *Injector) Resolve(ctx context.Context) Fault {
	f := i.base
	if !i.enabled {
		return f
	}
	bag := baggage.FromContext(ctx)
	if !i.targets(bag.Member(KeyTarget).Value()) {
		return f
	}
	if v, err := strconv.ParseFloat(bag.Member(KeyErrorRate).Value(), 64); err == nil {
		f.ErrorRate = v
	}
	if v, err := strconv.Atoi(bag.Member(KeyLatencyMS).Value()); err == nil {
		f.Latency = time.Duration(v) * time.Millisecond
	}
	return clamp(f)
}

func (i *Injector) targets(list string) bool {
	for t := range strings.SplitSeq(list, "|") {
		if t = strings.TrimSpace(t); t == "*" || t == i.service {
			return true
		}
	}
	return false
}

// Middleware applies the resolved fault before calling next.
func (i *Injector) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		f := i.Resolve(ctx)
		if !f.Active() {
			next.ServeHTTP(w, r)
			return
		}
		span := trace.SpanFromContext(ctx)
		if f.Latency > 0 {
			span.AddEvent("chaos.latency", trace.WithAttributes(attribute.Int64("chaos.latency_ms", f.Latency.Milliseconds())))
			t := time.NewTimer(f.Latency)
			select {
			case <-t.C:
			case <-ctx.Done():
				t.Stop()
				return
			}
		}
		if f.ErrorRate > 0 && i.rnd() < f.ErrorRate {
			span.AddEvent("chaos.error", trace.WithAttributes(attribute.Float64("chaos.error_rate", f.ErrorRate)))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "injected fault", "service": i.service})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func clamp(f Fault) Fault {
	f.ErrorRate = min(max(f.ErrorRate, 0), 1)
	f.Latency = min(max(f.Latency, 0), 30*time.Second)
	return f
}
