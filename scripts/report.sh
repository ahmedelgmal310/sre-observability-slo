#!/usr/bin/env bash
# Merge the k6 baseline summary into results.json and print a Markdown report
# (appended to the GitHub job summary when running in Actions).
# shellcheck source=scripts/lib.sh
source "$(dirname "$0")/lib.sh"

k6="${RESULTS_DIR}/baseline.json"
if [[ -f "$k6" ]]; then
  record baseline_requests "$(jq '.metrics.http_reqs.values.count' "$k6")"
  record baseline_rps "$(jq '.metrics.http_reqs.values.rate | . * 10 | round / 10' "$k6")"
  record baseline_client_p50_ms "$(jq '.metrics.http_req_duration.values.med | . * 10 | round / 10' "$k6")"
  record baseline_client_p95_ms "$(jq '.metrics.http_req_duration.values["p(95)"] | . * 10 | round / 10' "$k6")"
  record baseline_client_p99_ms "$(jq '.metrics.http_req_duration.values["p(99)"] | . * 10 | round / 10' "$k6")"
  record baseline_failed_ratio "$(jq '.metrics.http_req_failed.values.rate' "$k6")"
fi
record commit "${GITHUB_SHA:-local}"
record run_url "${GITHUB_SERVER_URL:-}/${GITHUB_REPOSITORY:-}/actions/runs/${GITHUB_RUN_ID:-}"

r="${RESULTS_DIR}/results.json"
pct() { jq -r --arg k "$1" '.[$k] | if type=="number" then (. * 10000 | round / 100 | tostring) + "%" else "n/a" end' "$r"; }
ms() { jq -r --arg k "$1" '.[$k] | if type=="number" then (. * 10000 | round / 10 | tostring) + " ms" else "n/a" end' "$r"; }
val() { jq -r --arg k "$1" '.[$k] // "n/a" | tostring' "$r"; }

report=$(cat <<EOF
## Lighthouse end-to-end results

| Check | Result |
|---|---|
| Baseline load | $(val baseline_requests) requests at $(val baseline_rps) req/s |
| Client p50 / p95 / p99 (k6) | $(val baseline_client_p50_ms) / $(val baseline_client_p95_ms) / $(val baseline_client_p99_ms) ms |
| Server p95 / p99 (gateway histogram) | $(ms baseline_server_p95_seconds) / $(ms baseline_server_p99_seconds) |
| Baseline 5xx ratio (SLO < 0.5%) | $(pct baseline_server_error_ratio) |
| Requests slower than 300ms (SLO < 5%) | $(pct baseline_slow_request_ratio) |
| Exemplar -> Tempo trace -> Loki logs | trace \`$(val exemplar_trace_id)\` spans 3 services, logs found |
| Chaos: injected 5xx ratio at gateway (5m) | $(pct chaos_error_ratio_5m) |
| Chaos: 1h burn rate | $(val chaos_burn_rate_1h)x |
| $(val alert_name) pending after | $(val alert_pending_after_seconds) s |
| $(val alert_name) firing after | $(val alert_firing_after_seconds) s |
| Alertmanager route | $(val alertmanager_receiver) |
EOF
)
echo "$report"
if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  echo "$report" >>"$GITHUB_STEP_SUMMARY"
fi
