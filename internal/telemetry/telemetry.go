// Package telemetry wires the OpenTelemetry SDK (traces, metrics, logs) for a
// service and installs the global providers and propagators.
//
// Exporters are configured through the standard OTEL_EXPORTER_OTLP_* and
// OTEL_METRIC_EXPORT_INTERVAL environment variables. When no OTLP endpoint is
// configured the SDK still runs (spans are sampled, metrics aggregated) but
// nothing leaves the process, which keeps unit tests and local runs hermetic.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// DurationMetric is the RED duration histogram recorded by every service.
const DurationMetric = "http.server.request.duration"

// DurationBuckets are the explicit histogram boundaries in seconds. 0.3 is a
// boundary on purpose: the gateway latency SLO is "requests faster than 300ms".
var DurationBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.15, 0.2, 0.25, 0.3, 0.4, 0.5, 0.75, 1, 2.5, 5,
}

// Config describes the service that emits telemetry.
type Config struct {
	ServiceName    string
	ServiceVersion string
	// Export enables OTLP/HTTP exporters. Endpoint, headers and intervals come
	// from the standard OTEL_* environment variables.
	Export bool
}

// Providers owns the SDK providers so they can be flushed on shutdown.
type Providers struct {
	Tracer *sdktrace.TracerProvider
	Meter  *sdkmetric.MeterProvider
	Logger *sdklog.LoggerProvider
}

// Setup builds the providers, registers them globally and returns them.
func Setup(ctx context.Context, cfg Config) (*Providers, error) {
	if cfg.ServiceName == "" {
		return nil, errors.New("telemetry: service name is required")
	}
	res := resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceVersion(cfg.ServiceVersion),
	)

	traceOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
	}
	metricOpts := []sdkmetric.Option{
		sdkmetric.WithResource(res),
		sdkmetric.WithExemplarFilter(exemplar.TraceBasedFilter),
		sdkmetric.WithView(sdkmetric.NewView(
			sdkmetric.Instrument{Name: DurationMetric},
			sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{Boundaries: DurationBuckets}},
		)),
	}
	logOpts := []sdklog.LoggerProviderOption{sdklog.WithResource(res)}

	if cfg.Export {
		te, err := otlptracehttp.New(ctx)
		if err != nil {
			return nil, fmt.Errorf("telemetry: trace exporter: %w", err)
		}
		me, err := otlpmetrichttp.New(ctx)
		if err != nil {
			return nil, fmt.Errorf("telemetry: metric exporter: %w", err)
		}
		le, err := otlploghttp.New(ctx)
		if err != nil {
			return nil, fmt.Errorf("telemetry: log exporter: %w", err)
		}
		traceOpts = append(traceOpts, sdktrace.WithBatcher(te, sdktrace.WithBatchTimeout(2*time.Second)))
		metricOpts = append(metricOpts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(me)))
		logOpts = append(logOpts, sdklog.WithProcessor(sdklog.NewBatchProcessor(le, sdklog.WithExportInterval(2*time.Second))))
	}

	p := &Providers{
		Tracer: sdktrace.NewTracerProvider(traceOpts...),
		Meter:  sdkmetric.NewMeterProvider(metricOpts...),
		Logger: sdklog.NewLoggerProvider(logOpts...),
	}
	otel.SetTracerProvider(p.Tracer)
	otel.SetMeterProvider(p.Meter)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	return p, nil
}

// Shutdown flushes and stops every provider, returning all errors joined.
func (p *Providers) Shutdown(ctx context.Context) error {
	return errors.Join(
		p.Tracer.Shutdown(ctx),
		p.Meter.Shutdown(ctx),
		p.Logger.Shutdown(ctx),
	)
}
