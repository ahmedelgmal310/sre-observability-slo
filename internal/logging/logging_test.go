package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type captureProcessor struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (c *captureProcessor) OnEmit(_ context.Context, r *sdklog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, r.Clone())
	return nil
}
func (c *captureProcessor) Enabled(context.Context, sdklog.EnabledParameters) bool { return true }
func (c *captureProcessor) Shutdown(context.Context) error                         { return nil }
func (c *captureProcessor) ForceFlush(context.Context) error                       { return nil }

func TestJSONIncludesTraceContext(t *testing.T) {
	var buf bytes.Buffer
	cp := &captureProcessor{}
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(cp))
	logger := New("gateway", &buf, slog.LevelInfo, lp)

	ctx, span := sdktrace.NewTracerProvider().Tracer("t").Start(context.Background(), "op")
	defer span.End()
	logger.With("k", "v").InfoContext(ctx, "hello", "x", 1)
	logger.WithGroup("g").Info("grouped", "y", 2)

	first, _, _ := bytes.Cut(buf.Bytes(), []byte("\n"))
	var line map[string]any
	if err := json.Unmarshal(first, &line); err != nil {
		t.Fatalf("not JSON: %q: %v", buf.String(), err)
	}
	if line["trace_id"] != span.SpanContext().TraceID().String() || line["span_id"] == nil {
		t.Fatalf("trace context missing: %v", line)
	}
	if line["service"] != "gateway" || line["k"] != "v" {
		t.Fatalf("attributes missing: %v", line)
	}
	if len(cp.records) != 2 || cp.records[0].TraceID() != span.SpanContext().TraceID() {
		t.Fatalf("OTel record missing trace context: %+v", cp.records)
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	cp := &captureProcessor{}
	logger := New("svc", &buf, slog.LevelWarn, sdklog.NewLoggerProvider(sdklog.WithProcessor(cp)))
	logger.Info("dropped")
	if buf.Len() != 0 || len(cp.records) != 0 {
		t.Fatalf("info should be filtered: %q, %d", buf.String(), len(cp.records))
	}
	logger.Warn("kept")
	if !strings.Contains(buf.String(), "kept") || len(cp.records) != 1 {
		t.Fatalf("warn should pass: %q, %d", buf.String(), len(cp.records))
	}
}

func TestWithoutProvider(t *testing.T) {
	var buf bytes.Buffer
	New("svc", &buf, slog.LevelInfo, nil).Info("plain")
	if strings.Contains(buf.String(), "trace_id") || !strings.Contains(buf.String(), "plain") {
		t.Fatalf("unexpected output %q", buf.String())
	}
}
