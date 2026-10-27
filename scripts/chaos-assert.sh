#!/usr/bin/env bash
# Inject faults in inventory (via W3C baggage sent by k6 to the gateway) and
# assert that the availability fast-burn alert fires in Prometheus and reaches
# Alertmanager on the paging route. Records how long detection took.
# shellcheck source=scripts/lib.sh
source "$(dirname "$0")/lib.sh"
mkdir -p "$RESULTS_DIR"

ALERT="${ALERT:-GatewayAvailabilityFastBurn}"
TIMEOUT="${TIMEOUT:-420}"
CONTAINER=lighthouse-k6-chaos

cleanup() { docker rm -f "$CONTAINER" >/dev/null 2>&1 || true; }
trap cleanup EXIT

log "starting chaos load (inventory error_rate=${CHAOS_ERROR_RATE:-0.3})"
docker compose run -d --name "$CONTAINER" \
  -e CHAOS_ERROR_RATE="${CHAOS_ERROR_RATE:-0.3}" k6 run /scripts/chaos.js >/dev/null
start=$(date +%s)
record chaos_started_at "$(date -u -d "@${start}" +%Y-%m-%dT%H:%M:%SZ)"

alert_state() {
  curl -fsS "${PROM_URL}/api/v1/alerts" |
    jq -r --arg a "$ALERT" '[.data.alerts[] | select(.labels.alertname==$a) | .state] | first // "inactive"'
}

pending_at=""
while :; do
  elapsed=$(($(date +%s) - start))
  state=$(alert_state)
  if [[ "$state" == "pending" && -z "$pending_at" ]]; then
    pending_at=$elapsed
    log "${ALERT} pending after ${elapsed}s"
  fi
  if [[ "$state" == "firing" ]]; then
    break
  fi
  if ((elapsed > TIMEOUT)); then
    docker logs --tail 20 "$CONTAINER" || true
    fail "${ALERT} did not fire within ${TIMEOUT}s (state=${state})"
  fi
  sleep 5
done
fired=$(($(date +%s) - start))
log "${ALERT} FIRING after ${fired}s of injected faults"
record alert_name "$ALERT"
record alert_pending_after_seconds "${pending_at:-$fired}"
record alert_firing_after_seconds "$fired"

err5m=$(promq 'slo:sli_error:ratio_rate5m{service="gateway",slo="availability"}')
burn=$(promq 'slo:sli_error:ratio_rate1h{service="gateway",slo="availability"} / on(service,slo) slo:error_budget:ratio')
log "gateway 5xx ratio (5m)=${err5m} burn rate (1h)=${burn}x"
record chaos_error_ratio_5m "$err5m"
record chaos_burn_rate_1h "$burn"

# The alert must also have been delivered to Alertmanager on the page route.
in_am() {
  curl -fsS --get "${AM_URL}/api/v2/alerts" --data-urlencode "filter=alertname=\"${ALERT}\"" |
    jq -e '[.[] | select(.status.state=="active") | select(.receivers[].name=="page")] | length > 0' >/dev/null
}
retry 60 in_am || fail "${ALERT} not active in Alertmanager on the page receiver"
log "${ALERT} active in Alertmanager, routed to receiver 'page'"
record alertmanager_receiver page

docker stop -t 15 "$CONTAINER" >/dev/null 2>&1 || true
docker logs --tail 3 "$CONTAINER" 2>/dev/null | grep -E '^chaos:' || true
