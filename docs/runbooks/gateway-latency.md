# Runbook: GatewayLatencyFastBurn / GatewayLatencySlowBurn

| | |
|---|---|
| **SLO** | 95% of gateway requests complete in under 300ms, rolling 30 days (budget: 5% of requests may be slower) |
| **Fast burn** | 1h and 5m share of slow requests both above 72% (14.4x). Page. |
| **Slow burn** | 6h and 30m share of slow requests both above 30% (6x). Page. |
| **Dashboard** | Grafana, *Lighthouse / Gateway SLOs* (`/d/lighthouse-slo`) |

## 1. Confirm impact

1. On the SLO dashboard, check **Gateway p95 latency vs 300ms target** and the
   **Latency burn rate** panel.
2. On *Lighthouse / Service RED*, **p95 latency by route** shows whether one
   route or every route is slow, and **Latency p50 / p95 / p99** shows whether the
   whole distribution moved (saturation) or only the tail (a slow dependency or
   lock contention).

## 2. Find the slow hop

Click an exemplar dot on the p95 line: it opens a real slow request in Tempo.
In the waterfall, the widest child span is where the time went. Alternatively:

```traceql
{ resource.service.name = "gateway" && duration > 300ms }
```

Compare per-service p95 to see which service added the latency:

```promql
histogram_quantile(0.95, sum by (job, le) (rate(http_server_request_duration_seconds_bucket[5m])))
```

Span events named `chaos.latency` mean the delay was injected.

## 3. Mitigate

| Cause | Action |
|---|---|
| Injected latency (`chaos.latency` span events) | Stop the experiment, or set `CHAOS_ENABLED=false`. |
| Saturation (all percentiles up, request rate up) | Scale the slow service out; shed load at the gateway if needed. |
| One dependency slow (only its spans are wide) | Fail fast: lower the client timeout, or degrade the feature that calls it. |
| Recent release | Roll back. |

## 4. Verify and close

- p95 back below 300ms on the dashboard and the 5m burn rate below 14.4x.
- Note budget spent from **Latency error budget left**; postmortem if more than 20%
  of the monthly budget was consumed.
