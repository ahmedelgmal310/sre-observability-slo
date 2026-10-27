# Gateway SLOs

The gateway is the only service users talk to, so it is the only service with
SLOs. Orders and inventory are measured (RED dashboards) but their failures only
matter to the extent they reach the edge.

## Objectives

| SLO | SLI | Objective | Window | Error budget |
|---|---|---|---|---|
| Availability | share of gateway requests **not** answered with 5xx | 99.5% | 30 days rolling | 0.5% of requests (about 3.6 h of total outage) |
| Latency | share of gateway requests completing in **under 300 ms** | 95% | 30 days rolling | 5% of requests may be slower |

Both SLIs are computed server-side from one OpenTelemetry histogram,
`http.server.request.duration` (Prometheus: `http_server_request_duration_seconds`),
recorded by every service with `http.route`, `http.request.method` and
`http.response.status_code` attributes. The histogram has an explicit `0.3`
bucket boundary so the latency SLI is exact rather than interpolated.

4xx responses count as successful for availability: they are the client's fault
and do not consume budget. All requests, including errors, count for latency.

## Recording rules

`observability/prometheus/rules/slo-gateway.yml` records, per SLO:

- `slo:sli_error:ratio_rate{5m,30m,1h,6h,30d}`: share of bad events over each window
- `slo:objective:ratio`, `slo:error_budget:ratio`: the targets, as series, so
  dashboards and rules never hard-code them twice
- `slo:error_budget_remaining:ratio`: `1 - (30d error ratio / budget)`
- `slo:burn_rate:ratio_rate1h`: how fast the budget is being spent right now

## Alerting: multi-window, multi-burn-rate

From the Google SRE Workbook, chapter 5 ("Alerting on SLOs"). A burn rate of 1
spends exactly the whole budget over 30 days.

| Alert | Long window | Short window | Burn rate | Threshold (availability / latency) | Budget spent when it fires | Severity |
|---|---|---|---|---|---|---|
| `*FastBurn` | 1h | 5m | 14.4x | 7.2% / 72% | 2% in 1 hour | page |
| `*SlowBurn` | 6h | 30m | 6x | 3% / 30% | 5% in 6 hours | page |

Why two windows per alert: the long window gives precision (a short blip does
not page), the short window gives a fast reset (once the problem is fixed the
alert clears within minutes instead of staying red for an hour). Unit test 3 in
`observability/prometheus/tests/slo-gateway.test.yml` demonstrates exactly that:
26 minutes into an incident that stopped at minute 20, the 1h ratio is still
above 7.2% but the page has cleared.

Fast-burn alerts use `for: 1m` to absorb one bad scrape; slow-burn alerts use
`for: 5m`. Alertmanager inhibits a slow-burn page while the fast-burn page for
the same SLO is firing.

## Testing the alerts

Two layers:

1. **Unit tests** (`promtool test rules`, runs in CI): synthetic series for
   healthy traffic, a 10% outage, a recovered outage, an 80% latency regression
   and a collector outage, asserting exactly which alerts fire and when.
2. **End to end** (k6 + chaos, runs in CI): real traffic through the real stack,
   with faults injected two hops deep in inventory through W3C baggage. CI
   asserts the fast-burn alert reaches `firing` in Prometheus and is delivered to
   the `page` receiver in Alertmanager, and records how long that took.

## Error budget policy (sketch)

- Budget above 50%: ship freely.
- Budget below 25%: releases need a rollback plan reviewed by on-call.
- Budget exhausted: feature freeze for the service; reliability work only until
  the 30-day SLI is back above objective.
- Any single incident spending more than 20% of the budget gets a postmortem.
