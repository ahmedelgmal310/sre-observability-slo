// Package logging builds the structured logger used by every service.
//
// Each record is written twice: as a JSON line on stdout (with trace_id and
// span_id taken from the context) and through the OpenTelemetry log bridge, so
// Loki receives it over OTLP with the trace context attached. The same trace_id
// therefore links a log line to its trace in Tempo and back.
package logging

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

// New returns a logger for service writing JSON to w and, when lp is not nil,
// to the OpenTelemetry logger provider.
func New(service string, w io.Writer, level slog.Leveler, lp log.LoggerProvider) *slog.Logger {
	handlers := []slog.Handler{
		&traceHandler{Handler: slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})},
	}
	if lp != nil {
		handlers = append(handlers, &levelHandler{
			Handler: otelslog.NewHandler(service, otelslog.WithLoggerProvider(lp)),
			level:   level,
		})
	}
	return slog.New(fanout(handlers)).With(slog.String("service", service))
}

// traceHandler adds trace_id and span_id from the context to every record.
type traceHandler struct{ slog.Handler }

func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, r)
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{h.Handler.WithAttrs(attrs)}
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{h.Handler.WithGroup(name)}
}

// levelHandler applies the configured minimum level to a handler that does
// not filter by itself.
type levelHandler struct {
	slog.Handler
	level slog.Leveler
}

func (h *levelHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.level.Level() && h.Handler.Enabled(ctx, l)
}

func (h *levelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &levelHandler{Handler: h.Handler.WithAttrs(attrs), level: h.level}
}

func (h *levelHandler) WithGroup(name string) slog.Handler {
	return &levelHandler{Handler: h.Handler.WithGroup(name), level: h.level}
}

// fanout sends each record to every enabled handler.
type fanout []slog.Handler

func (f fanout) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range f {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (f fanout) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range f {
		if h.Enabled(ctx, r.Level) {
			errs = append(errs, h.Handle(ctx, r.Clone()))
		}
	}
	return errors.Join(errs...)
}

func (f fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make(fanout, len(f))
	for i, h := range f {
		out[i] = h.WithAttrs(attrs)
	}
	return out
}

func (f fanout) WithGroup(name string) slog.Handler {
	out := make(fanout, len(f))
	for i, h := range f {
		out[i] = h.WithGroup(name)
	}
	return out
}
