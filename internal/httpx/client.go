package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

// NewClient returns an HTTP client that creates a client span per request and
// injects trace context and baggage into the outgoing headers.
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: &tracingTransport{base: http.DefaultTransport, tracer: otel.Tracer(instrumentationName)},
	}
}

type tracingTransport struct {
	base   http.RoundTripper
	tracer trace.Tracer
}

func (t *tracingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, span := t.tracer.Start(req.Context(), req.Method,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			semconv.HTTPRequestMethodKey.String(req.Method),
			semconv.ServerAddress(req.URL.Hostname()),
			semconv.URLFull(req.URL.String()),
		),
	)
	defer span.End()

	req = req.Clone(ctx)
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	span.SetAttributes(semconv.HTTPResponseStatusCode(resp.StatusCode))
	if resp.StatusCode >= http.StatusInternalServerError {
		span.SetStatus(codes.Error, http.StatusText(resp.StatusCode))
	}
	return resp, nil
}

// StatusError is returned when the upstream answers with a non-2xx status.
type StatusError struct {
	Status int
	Body   string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("upstream returned %d: %s", e.Status, e.Body)
}

// UpstreamStatus maps an upstream error to the status the caller should
// return: client errors pass through, everything else becomes 502.
func UpstreamStatus(err error) int {
	var se *StatusError
	if errors.As(err, &se) && se.Status >= 400 && se.Status < 500 {
		return se.Status
	}
	return http.StatusBadGateway
}

// DoJSON sends a JSON request (body may be nil) and decodes a 2xx JSON response
// into out. Non-2xx responses return *StatusError.
func DoJSON(ctx context.Context, c *http.Client, method, url string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &StatusError{Status: resp.StatusCode, Body: string(bytes.TrimSpace(msg))}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
