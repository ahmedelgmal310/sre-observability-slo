package telemetry

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
)

func TestSetupRequiresName(t *testing.T) {
	if _, err := Setup(context.Background(), Config{}); err == nil {
		t.Fatal("expected error for empty service name")
	}
}

func TestSetupWithoutExport(t *testing.T) {
	p, err := Setup(context.Background(), Config{ServiceName: "svc", ServiceVersion: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if otel.GetTracerProvider() != p.Tracer || otel.GetMeterProvider() != p.Meter {
		t.Fatal("global providers were not installed")
	}
	_, span := otel.Tracer("t").Start(context.Background(), "op")
	if !span.SpanContext().IsSampled() {
		t.Fatal("root spans must be sampled")
	}
	span.End()
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSetupWithExport(t *testing.T) {
	// Point at a closed port: constructing exporters must not dial.
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	p, err := Setup(context.Background(), Config{ServiceName: "svc", Export: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = p.Shutdown(ctx) // flushing to a closed port may fail; it must not hang.
}

func TestBucketsContainSLOThreshold(t *testing.T) {
	for _, b := range DurationBuckets {
		if b == 0.3 {
			return
		}
	}
	t.Fatal("0.3s bucket boundary is required by the latency SLO")
}
