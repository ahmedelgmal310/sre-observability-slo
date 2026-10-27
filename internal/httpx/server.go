// Package httpx holds the HTTP plumbing shared by every service: server-side
// instrumentation (spans, RED metrics, access logs), a trace-propagating
// client and small JSON helpers.
package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/ahmedelgmal310/sre-observability-slo/internal/telemetry"
)

const instrumentationName = "github.com/ahmedelgmal310/sre-observability-slo/internal/httpx"

// Instrumentation records a span, a duration histogram sample and an access
// log line for every request served through it.
type Instrumentation struct {
	tracer   trace.Tracer
	duration metric.Float64Histogram
	logger   *slog.Logger
}

// NewInstrumentation uses the global tracer and meter providers.
func NewInstrumentation(logger *slog.Logger) (*Instrumentation, error) {
	duration, err := otel.Meter(instrumentationName).Float64Histogram(
		telemetry.DurationMetric,
		metric.WithUnit("s"),
		metric.WithDescription("Duration of HTTP server requests."),
		metric.WithExplicitBucketBoundaries(telemetry.DurationBuckets...),
	)
	if err != nil {
		return nil, err
	}
	return &Instrumentation{
		tracer:   otel.Tracer(instrumentationName),
		duration: duration,
		logger:   logger,
	}, nil
}

// Handle registers h on mux for pattern ("METHOD /path/{param}"), wrapped with
// instrumentation. The route label is the path part of the pattern, so metric
// cardinality stays bounded.
func (in *Instrumentation) Handle(mux *http.ServeMux, pattern string, h http.Handler) {
	route := pattern
	if _, path, ok := strings.Cut(pattern, " "); ok {
		route = path
	}
	mux.Handle(pattern, in.Wrap(route, h))
}

// Wrap instruments h, labelling telemetry with route.
func (in *Instrumentation) Wrap(route string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := in.tracer.Start(ctx, r.Method+" "+route,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				semconv.HTTPRequestMethodKey.String(r.Method),
				semconv.HTTPRoute(route),
				semconv.URLPath(r.URL.Path),
			),
		)
		defer span.End()

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(rec, r.WithContext(ctx))

		elapsed := time.Since(start)
		span.SetAttributes(semconv.HTTPResponseStatusCode(rec.status))
		if rec.status >= http.StatusInternalServerError {
			span.SetStatus(codes.Error, http.StatusText(rec.status))
		}
		in.duration.Record(ctx, elapsed.Seconds(), metric.WithAttributes(
			attribute.String("http.request.method", r.Method),
			attribute.String("http.route", route),
			attribute.Int("http.response.status_code", rec.status),
		))

		level := slog.LevelInfo
		if rec.status >= http.StatusInternalServerError {
			level = slog.LevelError
		}
		in.logger.LogAttrs(ctx, level, "request completed",
			slog.String("method", r.Method),
			slog.String("route", route),
			slog.Int("status", rec.status),
			slog.Float64("duration_ms", float64(elapsed.Microseconds())/1000),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wroteHeader = true
	return s.ResponseWriter.Write(b)
}

// WriteJSON writes v as a JSON response with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes {"error": msg} with the given status.
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}
