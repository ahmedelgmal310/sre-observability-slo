#!/usr/bin/env bash
# Run after the baseline load test. Proves that each signal arrived and that
# the signals are linked:
#   metrics: RED metrics for all three services, server-side SLIs within SLO
#   exemplar: a histogram exemplar carries a trace_id
#   traces:  that trace exists in Tempo and spans gateway, orders, inventory
#   logs:    Loki has log lines from inventory carrying the same trace_id
#   alerts:  no SLO alert is firing under healthy load
#   grafana: datasources healthy, both dashboards provisioned
# shellcheck source=scripts/lib.sh
source "$(dirname "$0")/lib.sh"
mkdir -p "$RESULTS_DIR"

WINDOW="${WINDOW:-2m}"
M=http_server_request_duration_seconds

# ---- metrics ---------------------------------------------------------------
services=$(promq "count(count by (job) (${M}_count{job=~\"gateway|orders|inventory\"}))")
[[ "$services" == "3" ]] || fail "expected RED metrics from 3 services, got $services"
log "RED metrics present for gateway, orders, inventory"

p95=$(promq "histogram_quantile(0.95, sum by (le) (rate(${M}_bucket{job=\"gateway\"}[${WINDOW}])))")
p99=$(promq "histogram_quantile(0.99, sum by (le) (rate(${M}_bucket{job=\"gateway\"}[${WINDOW}])))")
err=$(promq "(sum(rate(${M}_count{job=\"gateway\",http_response_status_code=~\"5..\"}[${WINDOW}])) or vector(0)) / sum(rate(${M}_count{job=\"gateway\"}[${WINDOW}]))")
fast=$(promq "1 - sum(rate(${M}_bucket{job=\"gateway\",le=\"0.3\"}[${WINDOW}])) / sum(rate(${M}_count{job=\"gateway\"}[${WINDOW}]))")
log "server-side gateway p95=${p95}s p99=${p99}s error_ratio=${err} slower_than_300ms=${fast}"
lt "$p95" 0.3 || fail "gateway p95 ${p95}s breaches the 300ms SLO"
lt "$err" 0.005 || fail "gateway error ratio ${err} breaches the 99.5% SLO"
record baseline_server_p95_seconds "$p95"
record baseline_server_p99_seconds "$p99"
record baseline_server_error_ratio "$err"
record baseline_slow_request_ratio "$fast"

firing=$(curl -fsS "${PROM_URL}/api/v1/alerts" | jq '[.data.alerts[] | select(.labels.alertname | startswith("Gateway")) | select(.state=="firing")] | length')
[[ "$firing" == "0" ]] || fail "$firing SLO alerts firing under healthy load"
log "no SLO alerts firing under baseline load"

# ---- exemplar -> trace ------------------------------------------------------
now=$(date +%s)
trace_id=""
find_exemplar() {
  trace_id=$(curl -fsS --get "${PROM_URL}/api/v1/query_exemplars" \
    --data-urlencode "query=${M}_bucket{job=\"gateway\"}" \
    --data-urlencode "start=$((now - 600))" --data-urlencode "end=$(date +%s)" |
    jq -r '[.data[].exemplars[].labels.trace_id // empty] | last // empty')
  [[ -n "$trace_id" ]]
}
retry 60 find_exemplar || fail "no exemplars with trace_id on the gateway histogram"
log "exemplar trace_id=${trace_id}"
record exemplar_trace_id "$trace_id"

trace_services() {
  curl -fsS "${TEMPO_URL}/api/traces/${trace_id}" |
    jq -r '[.. | objects | select(.key? == "service.name") | .value.stringValue] | unique | join(",")'
}
check_trace() { [[ "$(trace_services 2>/dev/null)" == "gateway,inventory,orders" ]]; }
retry 90 check_trace || fail "trace ${trace_id} not found in Tempo with all 3 services (got: $(trace_services 2>/dev/null || echo none))"
log "Tempo trace ${trace_id} spans gateway, orders and inventory"

# ---- trace -> logs ------------------------------------------------------------
log_lines() {
  curl -fsS --get "${LOKI_URL}/loki/api/v1/query_range" \
    --data-urlencode "query={service_name=\"inventory\"} | trace_id=\"${trace_id}\"" \
    --data-urlencode "start=$(((now - 900) * 1000000000))" --data-urlencode "end=$(( ($(date +%s) + 60) * 1000000000 ))" \
    --data-urlencode "limit=10" | jq '[.data.result[].values[]] | length'
}
check_logs() { [[ "$(log_lines 2>/dev/null || echo 0)" -gt 0 ]]; }
retry 90 check_logs || fail "no Loki log lines from inventory with trace_id ${trace_id}"
log "Loki has inventory log lines for trace ${trace_id}"

total_logs=$(curl -fsS --get "${LOKI_URL}/loki/api/v1/query" \
  --data-urlencode 'query=sum(count_over_time({service_name=~"gateway|orders|inventory"}[10m]))' |
  jq -r '.data.result[0].value[1] // "0"')
record baseline_log_lines_10m "$total_logs"

# ---- service graph (Tempo metrics generator -> Prometheus remote write) ------
edges=$(promq 'count(traces_service_graph_request_total)')
if [[ "$edges" != "NaN" ]]; then
  log "service graph edges in Prometheus: ${edges}"
  record service_graph_edges "$edges"
else
  log "WARN: no service graph metrics yet (Grafana service map will be empty)"
fi

# ---- grafana ----------------------------------------------------------------------
auth=(-u "admin:${GRAFANA_ADMIN_PASSWORD:?GRAFANA_ADMIN_PASSWORD must be set}")
ds_healthy() {
  [[ "$(curl -fsS "${auth[@]}" "${GRAFANA_URL}/api/datasources/uid/$1/health" | jq -r '.status')" == "OK" ]]
}
for ds in prometheus tempo loki; do
  retry 60 ds_healthy "$ds" ||
    fail "Grafana datasource ${ds} unhealthy: $(curl -sS "${auth[@]}" "${GRAFANA_URL}/api/datasources/uid/${ds}/health")"
done
log "Grafana datasources prometheus, tempo, loki healthy"
dashboards=$(curl -fsS "${auth[@]}" "${GRAFANA_URL}/api/search?type=dash-db" | jq -r '[.[].uid] | sort | join(",")')
[[ "$dashboards" == *lighthouse-red* && "$dashboards" == *lighthouse-slo* ]] || fail "dashboards not provisioned: ${dashboards}"
log "Grafana dashboards provisioned: ${dashboards}"
