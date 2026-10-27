package httpx

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/ahmedelgmal310/sre-observability-slo/internal/telemetry"
)

var (
	spans  = tracetest.NewSpanRecorder()
	reader = sdkmetric.NewManualReader()
)

func TestMain(m *testing.M) {
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)))
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	os.Exit(m.Run())
}

func newInstr(t *testing.T, w io.Writer) *Instrumentation {
	t.Helper()
	in, err := NewInstrumentation(slog.New(slog.NewJSONHandler(w, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func histogramPoints(t *testing.T) []metricdata.HistogramDataPoint[float64] {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == telemetry.DurationMetric {
				return m.Data.(metricdata.Histogram[float64]).DataPoints
			}
		}
	}
	return nil
}

func TestInstrumentationRecordsREDAndSpans(t *testing.T) {
	var logs bytes.Buffer
	in := newInstr(t, &logs)
	mux := http.NewServeMux()
	in.Handle(mux, "GET /items/{id}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "bad" {
			WriteError(w, http.StatusInternalServerError, "boom")
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))

	for _, id := range []string{"1", "bad"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/items/"+id, nil))
	}

	statuses := map[int64]uint64{}
	for _, dp := range histogramPoints(t) {
		route, _ := dp.Attributes.Value("http.route")
		if route.AsString() != "/items/{id}" {
			continue
		}
		code, _ := dp.Attributes.Value(attribute.Key("http.response.status_code"))
		statuses[code.AsInt64()] += dp.Count
		if len(dp.Exemplars) == 0 {
			t.Errorf("expected exemplars on sampled requests, status %d", code.AsInt64())
		}
	}
	if statuses[200] != 1 || statuses[500] != 1 {
		t.Fatalf("unexpected status counts %v", statuses)
	}

	var sawError bool
	for _, s := range spans.Ended() {
		if s.Name() == "GET /items/{id}" && s.SpanKind() == trace.SpanKindServer && s.Status().Code == codes.Error {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("expected an errored server span")
	}
	if !strings.Contains(logs.String(), `"level":"ERROR"`) || !strings.Contains(logs.String(), `"status":200`) {
		t.Fatalf("access logs missing: %s", logs.String())
	}
}

func TestClientPropagatesContextAndBaggage(t *testing.T) {
	var gotTraceparent, gotBaggage string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTraceparent, gotBaggage = r.Header.Get("traceparent"), r.Header.Get("baggage")
		WriteJSON(w, http.StatusOK, map[string]string{"echo": r.Method})
	}))
	defer srv.Close()

	bag, _ := baggage.Parse("chaos.target=inventory")
	ctx := baggage.ContextWithBaggage(context.Background(), bag)
	ctx, parent := otel.Tracer("test").Start(ctx, "parent")
	defer parent.End()

	var out map[string]string
	if err := DoJSON(ctx, NewClient(0), http.MethodPost, srv.URL, map[string]int{"a": 1}, &out); err != nil {
		t.Fatal(err)
	}
	if out["echo"] != http.MethodPost {
		t.Fatalf("decoded %v", out)
	}
	if !strings.Contains(gotTraceparent, parent.SpanContext().TraceID().String()) {
		t.Fatalf("traceparent %q does not carry trace id", gotTraceparent)
	}
	if gotBaggage != "chaos.target=inventory" {
		t.Fatalf("baggage = %q", gotBaggage)
	}
}

func TestDoJSONErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/conflict":
			WriteError(w, http.StatusConflict, "nope")
		case "/down":
			WriteError(w, http.StatusServiceUnavailable, "down")
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()
	c := NewClient(0)
	ctx := context.Background()

	err := DoJSON(ctx, c, http.MethodGet, srv.URL+"/conflict", nil, nil)
	var se *StatusError
	if !errors.As(err, &se) || se.Status != http.StatusConflict || !strings.Contains(se.Error(), "nope") {
		t.Fatalf("conflict err = %v", err)
	}
	if UpstreamStatus(err) != http.StatusConflict {
		t.Fatalf("4xx should pass through, got %d", UpstreamStatus(err))
	}
	err = DoJSON(ctx, c, http.MethodGet, srv.URL+"/down", nil, nil)
	if UpstreamStatus(err) != http.StatusBadGateway {
		t.Fatalf("5xx should map to 502, got %d", UpstreamStatus(err))
	}
	if err := DoJSON(ctx, c, http.MethodGet, srv.URL+"/empty", nil, nil); err != nil {
		t.Fatalf("204 without out: %v", err)
	}
	if err := DoJSON(ctx, c, http.MethodGet, "http://127.0.0.1:1/", nil, nil); UpstreamStatus(err) != http.StatusBadGateway {
		t.Fatalf("dial error = %v", err)
	}
	if err := DoJSON(ctx, c, "BAD METHOD", srv.URL, nil, nil); err == nil {
		t.Fatal("expected invalid method error")
	}
	if err := DoJSON(ctx, c, http.MethodPost, srv.URL, func() {}, nil); err == nil {
		t.Fatal("expected marshal error")
	}
}

func TestStatusRecorderFirstWriteWins(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
	_, _ = rec.Write([]byte("x"))
	rec.WriteHeader(http.StatusTeapot)
	if rec.status != http.StatusOK {
		t.Fatalf("status = %d, implicit 200 must win", rec.status)
	}
}
