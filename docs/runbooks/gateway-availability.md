# Runbook: GatewayAvailabilityFastBurn / GatewayAvailabilitySlowBurn

| | |
|---|---|
| **SLO** | 99.5% of gateway requests are not 5xx, rolling 30 days (budget: 0.5%, about 3.6 hours of full outage) |
| **Fast burn** | 1h and 5m error ratio both above 7.2% (14.4x). 2% of the monthly budget is spent per hour. Page. |
| **Slow burn** | 6h and 30m error ratio both above 3% (6x). 5% of the budget per 6 hours. Page (inhibited while the fast burn fires). |
| **Dashboard** | Grafana, *Lighthouse / Gateway SLOs* (`/d/lighthouse-slo`) |

## 1. Confirm impact (2 minutes)

1. Open the SLO dashboard. Check the **Availability burn rate** panel: is the 5m
   line still above 14.4x, or has it already dropped (the incident may be over)?
2. Check user-facing scope in *Lighthouse / Service RED*, **5xx error ratio by
   service**. If only the gateway shows errors, the problem is at the edge. If
   orders or inventory also show 5xx, follow the errors downstream.
3. Note the start time from the **SLO alerts** state timeline for the incident record.

## 2. Find the failing hop

Errors propagate upward as `502` (gateway answers 502 when orders fails, orders
answers 502 when inventory fails). The deepest service returning `503`/`500` is
the origin.

```promql
sum by (job, http_response_status_code) (rate(http_server_request_duration_seconds_count{http_response_status_code=~"5.."}[5m]))
```

From the RED dashboard, click an exemplar on the latency panel during the error
window, or in Explore run a TraceQL query for failed requests:

```traceql
{ resource.service.name = "gateway" && status = error }
```

The trace shows which span failed first. Span events named `chaos.error`
mean the failure was injected on purpose (see step 4).

## 3. Read the logs for that trace

In the trace view, use **Logs for this span** (Tempo -> Loki link), or query:

```logql
{service_name=~"gateway|orders|inventory"} | trace_id="<trace id>"
```

Look for `upstream call failed`, `reservation failed` and `stock lookup failed`
with their `error` field.

## 4. Mitigate

| Cause | Action |
|---|---|
| Injected faults (`chaos.error` span events, `baggage: chaos.*` in requests) | Stop the chaos experiment. In shared environments set `CHAOS_ENABLED=false` and redeploy. |
| Inventory or orders crash-looping | `docker compose ps`, `docker compose logs <svc>`; restart the service. Roll back the last deploy if it started after a release. |
| Dependency timeouts | Check latency panels; a latency incident often shows up as 5xx when client timeouts (5s) trip. See [gateway-latency.md](gateway-latency.md). |
| Bad release | Roll back to the previous image tag. |

## 5. Verify and close

- The fast burn alert resolves once the 5m window recovers (within about 5
  minutes of the fix). The 1h ratio will remain elevated for a while; that is
  expected.
- Record budget spent: the **Availability error budget left** stat before and after.
- If more than 20% of the monthly budget was spent, open a postmortem and freeze
  risky releases until the budget recovers (error budget policy).
