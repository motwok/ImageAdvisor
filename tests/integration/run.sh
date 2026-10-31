#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# Integration test. Runs as root on a CI runner.
# Usage: run.sh IMAGE COVERDIR
# No pipefail: grep -q closes the pipe early and the writer would fail with SIGPIPE.
set -eu

IMAGE=$1
COVER=$2
WORK=/tmp/ia-it
DATA=$WORK/data
PORT=18080
URL=http://127.0.0.1:$PORT

mkdir -p "$WORK" "$DATA" "$COVER"
chmod 777 "$COVER"

step() { echo "== $*"; }
fail() {
  echo "FAIL: $*"
  echo "--- metrics"; curl -s "$URL/metrics" | grep -v '^#' || true
  echo "--- log"; docker logs imageadvisor 2>&1 | tail -80 || true
  exit 1
}
metrics() { curl -sf "$URL/metrics"; }
value() { metrics | grep "^imageadvisor_$1 " | awk '{print $2}'; }
wait_for() { # wait_for SECONDS DESCRIPTION COMMAND...
  local t=$1 d=$2; shift 2
  for _ in $(seq "$t"); do
    if "$@" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  fail "timeout: $d"
}

step "start"
cat > "$DATA/config.yml" <<EOF
global:
  check_interval: 2s
EOF
docker run -d --name imageadvisor \
  -e GOCOVERDIR=/cover -v "$COVER":/cover \
  -v /var/run/docker.sock:/var/run/docker.sock:ro -v "$DATA":/data \
  -p "$PORT":8080 "$IMAGE" >/dev/null
wait_for 60 "ready" curl -sf "$URL/-/ready"

step "metrics"
[ "$(value check_errors_total)" = 0 ] || fail "check errors"
metrics | grep -q '^imageadvisor_build_info{' || fail "build_info"

step "endpoints"
curl -sf "$URL/-/healthy" >/dev/null || fail "healthy"
[ "$(curl -s -o /dev/null -w '%{http_code}' -X POST "$URL/-/reload")" = 200 ] || fail "reload"
[ "$(value config_last_reload_successful)" = 1 ] || fail "reload metric"

step "invalid config keeps running"
echo "nope: 1" > "$DATA/config.yml"
docker kill -s HUP imageadvisor >/dev/null
reload_failed() { [ "$(value config_last_reload_successful)" = 0 ]; }
wait_for 30 "failed reload visible" reload_failed
curl -sf "$URL/-/ready" >/dev/null || fail "not ready after failed reload"

docker stop -t 10 imageadvisor >/dev/null
chmod -R a+rX "$COVER"
step "passed"
