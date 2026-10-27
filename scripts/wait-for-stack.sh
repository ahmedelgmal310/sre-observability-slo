#!/usr/bin/env bash
# Block until every component of the stack answers its readiness endpoint.
# shellcheck source=scripts/lib.sh
source "$(dirname "$0")/lib.sh"

wait_for gateway "${GATEWAY_URL}/readyz"
wait_for prometheus "${PROM_URL}/-/ready"
wait_for alertmanager "${AM_URL}/-/ready"
wait_for tempo "${TEMPO_URL}/ready" 240
wait_for loki "${LOKI_URL}/ready" 240
wait_for grafana "${GRAFANA_URL}/api/health"

# The collector is only reachable through Prometheus: wait for its target.
retry 120 bash -c "curl -fsS '${PROM_URL}/api/v1/targets?state=active' | jq -e '.data.activeTargets[] | select(.labels.job==\"otel-collector\" and .health==\"up\")' >/dev/null" ||
  fail "otel-collector target never became healthy"
log "otel-collector scrape target up"

# Send a request through all three services as a smoke test.
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' \
  -d '{"sku":"sku-1","qty":1}' "${GATEWAY_URL}/api/orders")
[[ "$code" == "201" ]] || fail "smoke order returned HTTP $code"
log "smoke order created through gateway -> orders -> inventory"
