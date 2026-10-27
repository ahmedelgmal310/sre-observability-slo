#!/usr/bin/env bash
# Shared helpers for the end-to-end scripts. Requires curl and jq.
set -euo pipefail

PROM_URL="${PROM_URL:-http://localhost:9090}"
AM_URL="${AM_URL:-http://localhost:9093}"
TEMPO_URL="${TEMPO_URL:-http://localhost:3200}"
LOKI_URL="${LOKI_URL:-http://localhost:3100}"
GRAFANA_URL="${GRAFANA_URL:-http://localhost:3000}"
GATEWAY_URL="${GATEWAY_URL:-http://localhost:8080}"
RESULTS_DIR="${RESULTS_DIR:-loadtest/results}"

log() { printf '[%s] %s\n' "$(date -u +%H:%M:%S)" "$*" >&2; }
fail() { log "FAIL: $*"; exit 1; }

# promq EXPR -> first sample value of an instant query, or "NaN" if empty.
promq() {
  curl -fsS --get "${PROM_URL}/api/v1/query" --data-urlencode "query=$1" |
    jq -r '.data.result[0].value[1] // "NaN"'
}

# wait_for NAME URL TIMEOUT_SECONDS
wait_for() {
  local name=$1 url=$2 timeout=${3:-180} start
  start=$(date +%s)
  until curl -fsS -o /dev/null "$url"; do
    if (( $(date +%s) - start > timeout )); then
      fail "$name not ready at $url after ${timeout}s"
    fi
    sleep 2
  done
  log "$name ready ($url)"
}

# retry TIMEOUT_SECONDS CMD... : rerun CMD every 3s until it succeeds.
retry() {
  local timeout=$1 start
  shift
  start=$(date +%s)
  until "$@"; do
    if (( $(date +%s) - start > timeout )); then
      return 1
    fi
    sleep 3
  done
}

# record KEY VALUE: merge a value into results.json.
record() {
  local file="${RESULTS_DIR}/results.json"
  [[ -f "$file" ]] || echo '{}' >"$file"
  jq --arg k "$1" --arg v "$2" '. + {($k): ($v | tonumber? // .)}' "$file" >"${file}.tmp"
  mv "${file}.tmp" "$file"
}

# lt A B: true when A < B (floating point).
lt() { awk -v a="$1" -v b="$2" 'BEGIN { exit !(a < b) }'; }
