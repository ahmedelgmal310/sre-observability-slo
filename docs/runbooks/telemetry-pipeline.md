# Runbook: TelemetryPipelineDown

Prometheus cannot scrape the OpenTelemetry Collector (`up{job="otel-collector"} == 0`
for 2 minutes). Service metrics stop arriving, so **every SLO alert is blind**
until this is fixed. Severity is ticket, but treat it as urgent during an
incident.

## Diagnose

1. Is the collector running?

   ```sh
   docker compose ps otel-collector
   docker compose logs --tail 100 otel-collector
   ```

2. Common log messages:
   - `memory_limiter ... dropping data`: the collector is over its 200 MiB limit.
     Check for a cardinality explosion (a new high-cardinality attribute on the
     duration histogram) or raise the limit.
   - Export errors to `tempo:4318` or `loki:3100`: a backend is down. Traces or
     logs are affected, metrics are not (they are pulled by Prometheus).
3. Check the target in Prometheus: *Status -> Targets*, job `otel-collector`.

## Mitigate

- Restart the collector: `docker compose restart otel-collector`.
- If config changed recently, validate it:
  `docker run --rm -v "$PWD/observability/otel-collector:/etc/otelcol:ro" otel/opentelemetry-collector-contrib:0.162.0 validate --config=/etc/otelcol/config.yaml`
  and roll back.

## After recovery

The SDKs buffer only briefly; metrics for the outage window are lost. Burn-rate
windows will under-count errors for that period. Mention the gap in any SLO
report covering it.
