#!/usr/bin/env bash
set -Eeuo pipefail

compose=(docker compose)
base_url="http://127.0.0.1:${WATCHDOG_PORT:-8080}"
target_url="http://127.0.0.1:18090"

cleanup() {
  if [[ "${KEEP_DEMO:-0}" != "1" ]]; then
    "${compose[@]}" down --volumes --remove-orphans
  fi
}
trap cleanup EXIT

wait_http() {
  local url="$1"
  for _ in {1..60}; do
    if curl --fail --silent --show-error "$url" >/dev/null; then return 0; fi
    sleep 2
  done
  echo "Timed out waiting for $url" >&2
  return 1
}

wait_state() {
  local expected="$1"
  for _ in {1..60}; do
    state="$(curl --fail --silent "$base_url/api/v1/endpoints/demo-http" | jq -r '.lastResult.state // "Unknown"')"
    if [[ "$state" == "$expected" ]]; then
      echo "demo-http reached $expected"
      return 0
    fi
    sleep 2
  done
  echo "Timed out waiting for demo-http=$expected; last state=$state" >&2
  return 1
}

cp -n .env.example .env
"${compose[@]}" up --detach --build
wait_http "$base_url/health/ready"
wait_state Healthy

curl --fail --silent --request POST "$target_url/control?mode=fail" >/dev/null
wait_state Unavailable
first_alert_count="$(curl --fail --silent "$base_url/api/v1/alerts" | jq '[.alerts[] | select(.endpointId == "demo-http" and .state == "Unavailable")] | length')"
sleep 6
second_alert_count="$(curl --fail --silent "$base_url/api/v1/alerts" | jq '[.alerts[] | select(.endpointId == "demo-http" and .state == "Unavailable")] | length')"
test "$first_alert_count" -eq 1
test "$second_alert_count" -eq 1
echo "alert deduplication verified"

"${compose[@]}" restart watchdog
wait_http "$base_url/health/ready"
wait_state Unavailable
echo "unavailable state survived watchdog restart"

curl --fail --silent --request POST "$target_url/control?mode=healthy" >/dev/null
wait_state Healthy
curl --fail --silent "$base_url/api/v1/transitions?endpoint=demo-http" \
  | jq -e '.transitions | any(.fromState == "Unavailable" and .toState == "Healthy")' >/dev/null
echo "recovery transition verified"

curl --fail --silent "$base_url/metrics" | grep -q '^watchdog_checks_total'
echo "controlled failure, restart, persistence, recovery, and metrics checks passed"

